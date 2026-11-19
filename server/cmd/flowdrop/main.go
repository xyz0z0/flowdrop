// Command flowdrop runs the FlowDrop Hub.
//
// Subcommands:
//
//	flowdrop serve [-config path]     start the hub (default)
//	flowdrop pair [-config path]      generate a one-time device pairing code
//	flowdrop login                      pair this machine and save the CLI credentials
//	flowdrop send FILE...|--text|--link  send items to the hub from the terminal
//	flowdrop doctor                   check data dir, DB, pairing and free space
//	flowdrop version                  print version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"flowdrop/internal/config"
	"flowdrop/internal/db"
	"flowdrop/internal/discovery"
	"flowdrop/internal/netbind"
	"flowdrop/internal/pairing"
	"flowdrop/internal/storage"
	"flowdrop/pkg/hub"
)

// version is overridden with -ldflags "-X main.version=..."
var version = "0.1.0-dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

// run executes one command line and returns the process exit code, so tests can
// drive the CLI without spawning a binary.
func run(args []string) int {
	cmd := "serve"
	if len(args) > 0 && !isFlag(args[0]) {
		cmd = args[0]
		args = args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "pair":
		err = runPair(args)
	case "doctor":
		err = runDoctor(args)
	case "backup":
		err = runBackup(args)
	case "login":
		return runLogin(args)
	case "send":
		return runSend(args)
	case "version", "--version", "-v":
		fmt.Println("flowdrop", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "flowdrop:", err)
		return 1
	}
	return 0
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

func usage() {
	fmt.Fprint(os.Stderr, `FlowDrop Hub

  flowdrop serve [-config FILE]      start the hub (default listen 127.0.0.1:8080)
                                     (multi-address auto binding: see flowdrop doctor)
  flowdrop pair [-config FILE]       create a one-time pairing code shown here only
                                     (2 minutes, single use, a new code voids the old one)
  flowdrop doctor [-config FILE]     check data dir, database, pairing state and free space
  flowdrop login [--hub URL] [--code CODE]  pair this machine and save device credentials
                                     (no --code: the code is generated in the local database)
  flowdrop send FILE... [--text TEXT|-] [--link URL]  send files, text or a link to the hub
  flowdrop backup OUT.tar.gz         consistent snapshot of database + attachments
  flowdrop version                   print version

Configuration comes from the JSON file (if present), then FLOWDROP_* env vars.
auto_bind=true also binds every eligible LAN/Tailscale IPv4 on the primary port.
See docs/DEPLOY.md for HTTPS, Tailscale Serve and systemd setup.
`)
}

func loadConfig(args []string) (config.Config, error) {
	fs := flag.NewFlagSet("flowdrop", flag.ContinueOnError)
	cfgPath := fs.String("config", hub.DefaultConfigPath(), "path to config JSON")
	if err := fs.Parse(args); err != nil {
		return config.Config{}, err
	}
	return config.Load(*cfgPath)
}

// runServe starts the Hub through the same embeddable package the Wails
// desktop bridge uses. The CLI keeps its flags and configuration loading; the
// Hub package owns binding, discovery, the janitor and graceful shutdown.
func runServe(args []string) error {
	fs := flag.NewFlagSet("flowdrop", flag.ContinueOnError)
	cfgPath := fs.String("config", hub.DefaultConfigPath(), "path to config JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	h, err := hub.New(hub.Options{ConfigPath: *cfgPath})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := h.Start(ctx); err != nil {
		// Start released the listener but not the database/lock: Stop releases
		// them so a retry (or a desktop instance) can take the data dir.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if serr := h.Stop(shutdownCtx); serr != nil {
			return errors.Join(err, serr)
		}
		return err
	}

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return h.Stop(shutdownCtx)
}

// runPair creates a pairing code and prints it on the local terminal only. It
// deliberately takes no hub lock: inserting a code into the shared database is
// how a running hub learns about it, so the hub must not be stopped or
// duplicated. Nothing is written to logs or files, and no token is echoed.
func runPair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	cfgPath := fs.String("config", hub.DefaultConfigPath(), "path to config JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	code, err := createPairingCode(cfg.DataDir, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("Pairing code: %s\n", code.Code)
	fmt.Printf("Valid for two minutes (until %s), single use.\n",
		time.UnixMilli(code.ExpiresAt).Format("15:04:05"))
	fmt.Println("A newly generated code voids this one. Enter it on the new device now.")
	return nil
}

// createPairingCode adds a fresh code to the hub that owns dataDir, dropping
// any previous code and resetting the failed-attempt counter.
func createPairingCode(dataDir string, now time.Time) (pairing.Code, error) {
	conn, err := db.Open(dataDir)
	if err != nil {
		return pairing.Code{}, err
	}
	defer conn.Close()
	return pairing.NewStore(conn).CreateCode(now)
}

func runDoctor(args []string) error {
	cfg, err := loadConfig(args)
	if err != nil {
		return err
	}
	ok := true
	report := func(name string, good bool, detail string) {
		status := "ok"
		if !good {
			status = "WARN"
			ok = false
		}
		fmt.Printf("%-22s %-4s %s\n", name, status, detail)
	}

	fmt.Println("FlowDrop doctor", version)
	listens := cfg.Listens()
	report("listen", true, cfg.Listen)
	for _, addr := range listens[1:] {
		report("additional listen", true, addr)
	}
	reportAutoBind(cfg, report)
	report("data_dir", true, cfg.DataDir)

	conn, err := db.Open(cfg.DataDir)
	if err != nil {
		report("database", false, err.Error())
		return fmt.Errorf("doctor found problems")
	}
	defer conn.Close()
	var v int
	_ = conn.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v)
	report("database", true, fmt.Sprintf("schema v%d", v))

	// Pairing state lives in the same database: the hub ID proves the pairing
	// tables are readable, and the device count is informational (zero paired
	// devices is a normal, freshly installed hub).
	pairStore := pairing.NewStore(conn)
	hubID, err := pairStore.HubID()
	if err != nil {
		report("pairing", false, err.Error())
	} else {
		report("pairing", true, "hub id "+hubID)
	}
	devices, err := pairStore.ListDevices()
	switch {
	case err != nil:
		report("paired devices", false, err.Error())
	case len(devices) == 0:
		report("paired devices", true, "0 paired (normal until a device pairs)")
	default:
		report("paired devices", true, fmt.Sprintf("%d paired", len(devices)))
	}

	files, err := storage.New(cfg.DataDir)
	if err != nil {
		report("storage", false, err.Error())
		return fmt.Errorf("doctor found problems")
	}
	avail, err := files.AvailableBytes()
	if err != nil {
		report("free space", false, err.Error())
	} else {
		enough := avail > uint64(cfg.MinFreeBytes)+uint64(cfg.MaxUploadBytes)
		report("free space", enough, humanBytes(int64(avail)))
	}

	// Bind every listener at the same time: a wildcard plus a concrete IP on the
	// same port is a real conflict the Hub would hit at Start, and sequential
	// probes would hide it.
	var probes []net.Listener
	defer func() {
		for _, ln := range probes {
			ln.Close()
		}
	}()
	for _, addr := range listens {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			report("port available", false, addr+": "+err.Error())
			break
		}
		probes = append(probes, ln)
		report("port available", true, "can bind "+addr)
	}

	if !cfg.AllListensLoopback() {
		if cfg.CookieSecure {
			fmt.Println()
			fmt.Println("NOTE: at least one listen address is not loopback while cookie_secure=true.")
			fmt.Println("      Access must go through Tailscale Serve or a TLS reverse proxy; a")
			fmt.Println("      plaintext LAN browser will not keep its session.")
		} else {
			fmt.Println()
			fmt.Println("NOTE: plaintext HTTP LAN mode (cookie_secure=false). Session cookies")
			fmt.Println("      and content are unencrypted on this network. Use only on a")
			fmt.Println("      trusted home Wi-Fi; prefer Tailscale or a TLS reverse proxy otherwise.")
		}
	}
	if !cfg.DiscoveryOn() {
		report("mdns discovery", true, "off: discovery_enabled=false")
	} else {
		for _, addr := range listens {
			if t, err := discovery.Inspect(addr, cfg.CookieSecure); err != nil {
				report("mdns discovery", true, "off for "+addr+": "+err.Error())
			} else {
				report("mdns discovery", true, fmt.Sprintf("%s on %s:%d via %s (listen %s)",
					discovery.ServiceTypeFQDN, t.IP, t.Port, t.Iface.Name, addr))
			}
		}
	}
	if len(cfg.AllowedHosts) == 0 && !cfg.AllListensLoopback() {
		report("allowed_hosts", false, "empty while listening on a non-loopback address")
	}
	fmt.Println()
	if !ok {
		return fmt.Errorf("doctor found problems")
	}
	fmt.Println("No blocking problems found.")
	return nil
}

// reportAutoBind shows what auto_bind would actually do right now. It only reads
// the config and this machine's addresses: nothing is bound, the Hub is not
// started and the data dir is untouched, so doctor stays a read-only probe.
func reportAutoBind(cfg config.Config, report func(name string, good bool, detail string)) {
	if !cfg.AutoBind {
		report("auto_bind", true, "off")
		return
	}
	listens := cfg.Listens()
	port := ""
	if len(listens) > 0 {
		if _, p, err := net.SplitHostPort(listens[0]); err == nil {
			port = p
		}
	}
	if port == "" || port == "0" || !isPortNumber(port) {
		report("auto_bind", false, "WARN: primary listener uses port 0; auto binding is disabled")
		return
	}
	addrs, err := netbind.Scan()
	if err != nil {
		report("auto_bind", false, "on but scan failed: "+err.Error())
		return
	}
	explicit := make(map[string]struct{}, len(listens))
	for _, addr := range listens {
		explicit[canonicalAddr(addr)] = struct{}{}
	}
	var targets []string
	for _, addr := range netbind.Targets(addrs, port) {
		if _, ok := explicit[canonicalAddr(addr)]; ok {
			continue // already covered by listen/additional_listen
		}
		targets = append(targets, addr)
	}
	if len(targets) == 0 {
		report("auto_bind", true, "on, no eligible LAN or Tailscale address right now")
		return
	}
	report("auto_bind", true, "on")
	for _, addr := range targets {
		report("auto_bind target", true, addr)
	}
}

func isPortNumber(port string) bool {
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return port != ""
}

// canonicalAddr normalises a "host:port" listener so an explicit address and an
// auto-bound one compare equal despite spelling (010.0.0.1:08080 vs 10.0.0.1:8080).
func canonicalAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	if n, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(n)
	}
	return net.JoinHostPort(host, port)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
