// Command flowdrop runs the FlowDrop Hub.
//
// Subcommands:
//
//	flowdrop serve [-config path]     start the hub (default)
//	flowdrop admin init [-username]   set/rotate the admin password locally
//	flowdrop doctor                   check data dir, DB, ports, free space
//	flowdrop version                  print version
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"flowdrop/internal/auth"
	"flowdrop/internal/config"
	"flowdrop/internal/db"
	"flowdrop/internal/discovery"
	"flowdrop/internal/storage"
	"flowdrop/pkg/hub"
	"golang.org/x/term"
)

// version is overridden with -ldflags "-X main.version=..."
var version = "0.1.0-dev"

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !isFlag(args[0]) {
		cmd = args[0]
		args = args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "admin":
		err = runAdmin(args)
	case "doctor":
		err = runDoctor(args)
	case "backup":
		err = runBackup(args)
	case "version", "--version", "-v":
		fmt.Println("flowdrop", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "flowdrop:", err)
		os.Exit(1)
	}
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }

func usage() {
	fmt.Fprint(os.Stderr, `FlowDrop Hub

  flowdrop serve [-config FILE]      start the hub (default listen 127.0.0.1:8080)
  flowdrop admin init [-config FILE] [-username NAME] [--password-stdin]
                                     set or rotate the admin password
                                     (--password-stdin: two lines on stdin, for scripts)
  flowdrop doctor [-config FILE]     check data dir, database and free space
  flowdrop backup OUT.tar.gz         consistent snapshot of database + attachments
  flowdrop version                   print version

Configuration comes from the JSON file (if present), then FLOWDROP_* env vars.
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

func runAdmin(args []string) error {
	if len(args) == 0 || args[0] != "init" {
		return errors.New("usage: flowdrop admin init [-username NAME] [-config FILE]")
	}
	fs := flag.NewFlagSet("admin init", flag.ContinueOnError)
	cfgPath := fs.String("config", hub.DefaultConfigPath(), "path to config JSON")
	username := fs.String("username", "admin", "admin username")
	force := fs.Bool("force", false, "rotate without prompting for confirmation")
	passwordStdin := fs.Bool("password-stdin", false,
		"read the password from stdin (two lines) instead of the terminal, for scripts/CI")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	conn, err := db.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer conn.Close()
	sess := auth.NewStore(conn, cfg.SessionTTL())

	has, err := sess.HasAdmin()
	if err != nil {
		return err
	}
	if has && !*force {
		fmt.Fprintf(os.Stderr, "An admin password already exists. Rotating it will revoke all sessions.\nContinue? [y/N] ")
		var answer string
		fmt.Fscanln(os.Stdin, &answer)
		if answer != "y" && answer != "Y" && answer != "yes" {
			fmt.Println("aborted")
			return nil
		}
	}

	var password string
	if *passwordStdin {
		password, err = readPasswordFromStdin()
	} else {
		password, err = readPasswordTwice()
	}
	if err != nil {
		return err
	}
	if err := sess.SetPassword(*username, password); err != nil {
		return err
	}
	if has {
		if _, err := conn.Exec(`DELETE FROM sessions`); err != nil {
			return err
		}
	}
	fmt.Printf("Admin password set for user %q in %s\n", *username, cfg.DataDir)
	return nil
}

// readPasswordFromStdin reads the password and its confirmation as two lines.
// Intended for automation; the value still never appears in logs or process
// arguments (unlike passing it as a flag).
func readPasswordFromStdin() (string, error) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	var lines []string
	for len(lines) < 2 && sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if len(lines) < 2 {
		return "", errors.New("expected two lines on stdin (password, then confirmation)")
	}
	if len(lines[0]) < 10 {
		return "", errors.New("password must be at least 10 characters")
	}
	if lines[0] != lines[1] {
		return "", errors.New("passwords do not match")
	}
	return lines[0], nil
}

func readPasswordTwice() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("stdin is not a terminal; run admin init interactively")
	}
	fmt.Fprint(os.Stderr, "New admin password: ")
	p1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if len(p1) < 10 {
		return "", errors.New("password must be at least 10 characters")
	}
	fmt.Fprint(os.Stderr, "Repeat password: ")
	p2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(p1) != string(p2) {
		return "", errors.New("passwords do not match")
	}
	return string(p1), nil
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

	sess := auth.NewStore(conn, cfg.SessionTTL())
	has, err := sess.HasAdmin()
	report("admin password", has && err == nil, map[bool]string{true: "configured", false: "missing — run 'flowdrop admin init'"}[has && err == nil])

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
			fmt.Println("NOTE: plaintext HTTP LAN mode (cookie_secure=false). Passwords, session")
			fmt.Println("      cookies and content are unencrypted on this network. Use only on a")
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
