// CLI wiring for the `login` and `send` subcommands. Both use the frozen
// sendcli HTTP client; nothing here talks to the Hub directly.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"flowdrop/internal/config"
	"flowdrop/internal/sendcli"
	"flowdrop/pkg/hub"
)

const (
	// loginTimeout bounds pairing plus the locally generated code.
	loginTimeout = 30 * time.Second
	// connectTimeout bounds the session handshake.
	connectTimeout = 30 * time.Second
	// uploadTimeout bounds one item; large uploads are not cut off early.
	uploadTimeout = 10 * time.Minute
	// maxDeviceName matches the Hub's sender name limit.
	maxDeviceName = 64
	// maxTextBytes mirrors the Hub's text limit; one extra byte detects
	// oversized stdin so no request is sent.
	maxTextBytes = 64 << 10
)

// errNotLoggedIn is reported when no token is available at all.
var errNotLoggedIn = errors.New("未登录，先运行 flowdrop login")

// runLogin pairs this machine with a Hub and saves the device credentials.
func runLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	hubFlag := fs.String("hub", "", "hub base URL (default: $FLOWDROP_HUB, then the local config)")
	codeFlag := fs.String("code", "", "pairing code from the hub (default: generate one locally)")
	nameFlag := fs.String("name", "", "device name (default: cli-<hostname>)")
	credsFlag := fs.String("credentials", "", "credentials file (default: $FLOWDROP_CLI_CONFIG)")
	cfgFlag := fs.String("config", hub.DefaultConfigPath(), "path to config JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "login: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	cfg, err := config.Load(*cfgFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flowdrop:", err)
		return 1
	}
	hubURL, err := resolveHubURL(*hubFlag, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flowdrop:", err)
		return 1
	}

	code := strings.TrimSpace(*codeFlag)
	if code == "" {
		// Same-machine shortcut: create the code in the local database, which
		// is exactly how a running hub learns about it.
		c, err := createPairingCode(cfg.DataDir, time.Now())
		if err != nil {
			fmt.Fprintf(os.Stderr, "flowdrop: cannot generate a pairing code in %s: %v\n", cfg.DataDir, err)
			fmt.Fprintln(os.Stderr, "请用 --code 传入设置窗口『添加设备』生成的码")
			fmt.Fprintln(os.Stderr, "(pass --code with the code shown by the desktop settings)")
			return 1
		}
		code = c.Code
	}

	name := strings.TrimSpace(*nameFlag)
	if name == "" {
		name = defaultDeviceName()
	}

	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()
	creds, err := sendcli.Pair(ctx, hubURL, code, name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flowdrop:", err)
		return 1
	}

	path := strings.TrimSpace(*credsFlag)
	if path == "" {
		path = sendcli.DefaultCredentialsPath()
	}
	if err := sendcli.SaveCredentials(path, creds); err != nil {
		fmt.Fprintln(os.Stderr, "flowdrop:", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "hub          %s\n", creds.HubURL)
	fmt.Fprintf(os.Stdout, "device       %s\n", creds.DeviceName)
	fmt.Fprintf(os.Stdout, "device_id    %s\n", creds.DeviceID)
	fmt.Fprintf(os.Stdout, "credentials  %s\n", path)
	fmt.Fprintln(os.Stdout, "可在桌面设置里撤销此设备 (revoke this device in the desktop settings)")
	return 0
}

// resolveHubURL picks the hub address: --hub, then FLOWDROP_HUB, then the
// first listen address of the local config (wildcards become loopback).
func resolveHubURL(flagURL string, cfg config.Config) (string, error) {
	if v := strings.TrimSpace(flagURL); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv("FLOWDROP_HUB")); v != "" {
		return v, nil
	}
	for _, addr := range cfg.Listens() {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || port == "" {
			continue
		}
		switch host {
		case "", "0.0.0.0", "::":
			host = "127.0.0.1"
		}
		scheme := "http"
		if cfg.CookieSecure {
			scheme = "https"
		}
		return scheme + "://" + net.JoinHostPort(host, port), nil
	}
	return "", errors.New("no hub URL: pass --hub, set FLOWDROP_HUB, or set listen in the config file\n" +
		"  example: flowdrop login --hub http://127.0.0.1:8080 --code 1234")
}

// defaultDeviceName builds cli-<hostname>, capped at the Hub's name limit.
func defaultDeviceName() string {
	name := "cli"
	if hn, err := os.Hostname(); err == nil {
		if h := strings.TrimSpace(hn); h != "" {
			name = "cli-" + h
		}
	}
	return truncateBytes(name, maxDeviceName)
}

// truncateBytes cuts s to at most n bytes without splitting a UTF-8 rune.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// sendJob is one input: a file (or stdin spooled to a file), a text or a link.
type sendJob struct {
	// input is the result label: the original argument, "stdin" or "link".
	input string
	// kind is "file", "stdin-file", "text", "stdin-text" or "link".
	kind string
	path string
	text string
}

// sendOutcome is one rendered result line.
type sendOutcome struct {
	input   string
	ok      bool
	item    *sendcli.Item
	size    int64
	sha256  string
	code    string
	message string
}

func (o sendOutcome) failed() bool { return !o.ok }

// sendFailure builds a failed outcome with a Hub-style error code.
func sendFailure(input, code, message string) sendOutcome {
	return sendOutcome{input: input, code: code, message: message}
}

// runSend sends one file set, one text or one link to the Hub.
func runSend(args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	hubFlag := fs.String("hub", "", "hub base URL (default: credentials, then $FLOWDROP_HUB)")
	jsonOut := fs.Bool("json", false, "one NDJSON result line per input")
	textFlag := fs.String("text", "", "send TEXT as a text item; - reads stdin")
	linkFlag := fs.String("link", "", "send URL as a link item")
	nameFlag := fs.String("name", "", "file name for - (stdin) uploads")
	credsFlag := fs.String("credentials", "", "credentials file (default: $FLOWDROP_CLI_CONFIG)")
	// Accepted for symmetry with the other subcommands; send resolves its hub
	// from --hub, FLOWDROP_HUB or the saved credentials only.
	_ = fs.String("config", hub.DefaultConfigPath(), "path to config JSON (accepted for symmetry)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	textSet, linkSet := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "text":
			textSet = true
		case "link":
			linkSet = true
		}
	})
	positional := fs.Args()
	name := strings.TrimSpace(*nameFlag)

	modes := 0
	if textSet {
		modes++
	}
	if linkSet {
		modes++
	}
	if len(positional) > 0 {
		modes++
	}
	if modes != 1 {
		fmt.Fprintln(os.Stderr, "send: exactly one of FILE..., --text or --link is required")
		sendUsage()
		return 2
	}

	var jobs []sendJob
	switch {
	case textSet:
		if name != "" {
			fmt.Fprintln(os.Stderr, "send: --name is only allowed with - (stdin)")
			sendUsage()
			return 2
		}
		if *textFlag == "-" {
			jobs = append(jobs, sendJob{input: "stdin", kind: "stdin-text"})
		} else {
			jobs = append(jobs, sendJob{input: "text", kind: "text", text: *textFlag})
		}
	case linkSet:
		if name != "" {
			fmt.Fprintln(os.Stderr, "send: --name is only allowed with - (stdin)")
			sendUsage()
			return 2
		}
		jobs = append(jobs, sendJob{input: "link", kind: "link", text: *linkFlag})
	default:
		stdinFiles := 0
		for _, p := range positional {
			if p == "-" {
				stdinFiles++
			}
		}
		if stdinFiles > 1 {
			fmt.Fprintln(os.Stderr, "send: - (stdin) may be given only once")
			sendUsage()
			return 2
		}
		if stdinFiles == 1 && name == "" {
			fmt.Fprintln(os.Stderr, "send: --name is required when reading a file from stdin (-)")
			sendUsage()
			return 2
		}
		if stdinFiles == 0 && name != "" {
			fmt.Fprintln(os.Stderr, "send: --name is only allowed with - (stdin)")
			sendUsage()
			return 2
		}
		for _, p := range positional {
			if p == "-" {
				jobs = append(jobs, sendJob{input: "-", kind: "stdin-file"})
				continue
			}
			jobs = append(jobs, sendJob{input: p, kind: "file", path: p})
		}
	}

	hubURL, token, sender, err := resolveSendAuth(*hubFlag, *credsFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flowdrop:", err)
		return 1
	}
	client, err := sendcli.NewClient(hubURL, token)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flowdrop:", err)
		return 1
	}
	// The display label the Web inbox shows for this item.
	client.SetSenderDevice(sender)
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), connectTimeout)
	err = client.Connect(connectCtx)
	cancelConnect()
	if err != nil {
		if errors.Is(err, sendcli.ErrUnauthorized) {
			fmt.Fprintln(os.Stderr, "flowdrop: device token was rejected (revoked in settings?); run `flowdrop login` again")
			return 1
		}
		fmt.Fprintln(os.Stderr, "flowdrop:", err)
		return 1
	}

	outcomes := make([]sendOutcome, 0, len(jobs))
	rateLimited := false
	for _, job := range jobs {
		if rateLimited {
			// Stop after a rate limit; report the rest as skipped, unsent.
			outcomes = append(outcomes, sendFailure(job.input, "skipped", "not sent: rate limited"))
			continue
		}
		o, limited := runSendJob(client, job, client.MaxUploadBytes(), name)
		outcomes = append(outcomes, o)
		if limited {
			rateLimited = true
		}
	}

	renderSendOutcomes(outcomes, *jsonOut)
	for _, o := range outcomes {
		if o.failed() {
			return 1
		}
	}
	return 0
}

// resolveSendAuth returns the hub URL, device token and the sender label. The
// label is the paired device name from the credentials file, or
// defaultDeviceName() when the token comes from FLOWDROP_TOKEN/FLOWDROP_HUB
// (that path has no device name) or when the saved credentials carry none.
// FLOWDROP_TOKEN together with FLOWDROP_HUB wins over the saved credentials and
// is never written to disk; --hub overrides the hub either way.
func resolveSendAuth(hubFlag, credsFlag string) (string, string, string, error) {
	envHub := strings.TrimSpace(os.Getenv("FLOWDROP_HUB"))
	envToken := strings.TrimSpace(os.Getenv("FLOWDROP_TOKEN"))
	flagHub := strings.TrimSpace(hubFlag)
	if envToken != "" && envHub != "" {
		hub := flagHub
		if hub == "" {
			hub = envHub
		}
		return hub, envToken, defaultDeviceName(), nil
	}

	path := strings.TrimSpace(credsFlag)
	if path == "" {
		path = sendcli.DefaultCredentialsPath()
	}
	creds, err := sendcli.LoadCredentials(path)
	if err != nil {
		if errors.Is(err, sendcli.ErrNoCredentials) {
			return "", "", "", fmt.Errorf("%w (no credentials at %s)", errNotLoggedIn, path)
		}
		return "", "", "", err
	}
	hub := flagHub
	if hub == "" {
		hub = creds.HubURL
	}
	sender := strings.TrimSpace(creds.DeviceName)
	if sender == "" {
		sender = defaultDeviceName()
	}
	return hub, creds.DeviceToken, sender, nil
}

// runSendJob processes one input. The second result reports a rate limit, which
// stops the remaining inputs.
func runSendJob(client *sendcli.Client, job sendJob, limit int64, name string) (sendOutcome, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()

	switch job.kind {
	case "stdin-text":
		text, err := readStdinText(os.Stdin)
		if err != nil {
			return sendFailure(job.input, "text_too_large", err.Error()), false
		}
		item, err := client.SendText(ctx, "text", text)
		if err != nil {
			return classifySendError(job.input, err)
		}
		return sendSuccess(job.input, item, int64(len(text)), ""), false
	case "text", "link":
		kind := job.kind
		item, err := client.SendText(ctx, kind, job.text)
		if err != nil {
			return classifySendError(job.input, err)
		}
		return sendSuccess(job.input, item, int64(len(job.text)), ""), false
	}

	// File inputs: a regular file, or stdin spooled to a temp file.
	path := job.path
	if job.kind == "stdin-file" {
		tmp, err := os.CreateTemp("", "flowdrop-send-*")
		if err != nil {
			return sendFailure(job.input, "spool_failed", err.Error()), false
		}
		path = tmp.Name()
		defer os.Remove(path)
		if _, err := io.Copy(tmp, os.Stdin); err != nil {
			tmp.Close()
			return sendFailure(job.input, "spool_failed", fmt.Sprintf("read stdin: %v", err)), false
		}
		if err := tmp.Close(); err != nil {
			return sendFailure(job.input, "spool_failed", fmt.Sprintf("close spool file: %v", err)), false
		}
	}

	st, err := os.Stat(path)
	if err != nil {
		return sendFailure(job.input, "stat_failed", err.Error()), false
	}
	if !st.Mode().IsRegular() {
		return sendFailure(job.input, "not_regular_file", "not a regular file"), false
	}
	if limit > 0 && st.Size() > limit {
		return sendFailure(job.input, "too_large",
			fmt.Sprintf("file exceeds the hub upload limit of %s", humanBytes(limit))), false
	}

	var item sendcli.Item
	if job.kind == "stdin-file" {
		f, err := os.Open(path)
		if err != nil {
			return sendFailure(job.input, "stat_failed", err.Error()), false
		}
		defer f.Close()
		item, err = client.SendReader(ctx, name, st.Size(), f)
		if err != nil {
			return classifySendError(job.input, err)
		}
	} else {
		item, err = client.SendFile(ctx, path)
		if err != nil {
			return classifySendError(job.input, err)
		}
	}

	size, sha := st.Size(), ""
	if item.Blob != nil {
		size = item.Blob.SizeBytes
		sha = item.Blob.SHA256
	}
	if sha == "" {
		sha, _ = sha256File(path)
	}
	return sendSuccess(job.input, item, size, sha), false
}

// sendSuccess builds a successful outcome from a Hub item.
func sendSuccess(input string, item sendcli.Item, size int64, sha string) sendOutcome {
	return sendOutcome{input: input, ok: true, item: &item, size: size, sha256: sha}
}

// classifySendError maps a client error to a code/message and flags the rate
// limit that must stop the remaining inputs.
func classifySendError(input string, err error) (sendOutcome, bool) {
	var apiErr *sendcli.Error
	if errors.As(err, &apiErr) {
		code := apiErr.Code
		if code == "" {
			code = fmt.Sprintf("http_%d", apiErr.Status)
		}
		limited := code == "rate_limited" || apiErr.Status == 429
		return sendFailure(input, code, apiErr.Message), limited
	}
	if errors.Is(err, sendcli.ErrUnauthorized) {
		return sendFailure(input, "unauthorized", "device token rejected; run flowdrop login again"), false
	}
	return sendFailure(input, "request_failed", err.Error()), false
}

// readStdinText reads at most maxTextBytes from r; one extra byte reports an
// oversized input before any request is made.
func readStdinText(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxTextBytes+1))
	if err != nil {
		return "", fmt.Errorf("read stdin: %v", err)
	}
	if len(b) > maxTextBytes {
		return "", fmt.Errorf("text exceeds %d bytes", maxTextBytes)
	}
	return string(b), nil
}

// sha256File hashes a local file; only used when the Hub omitted the digest.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// renderSendOutcomes writes one line per input: human-readable on stdout and
// errors on stderr, or strict NDJSON on stdout with --json.
func renderSendOutcomes(outcomes []sendOutcome, jsonOut bool) {
	if jsonOut {
		for _, o := range outcomes {
			line, err := json.Marshal(o.jsonResult())
			if err != nil {
				continue
			}
			fmt.Fprintf(os.Stdout, "%s\n", line)
		}
		return
	}
	for _, o := range outcomes {
		if o.ok {
			if o.sha256 != "" {
				fmt.Fprintf(os.Stdout, "%s  %s  sha256=%s  id=%s\n", o.input, humanBytes(o.size), o.sha256, o.item.ID)
			} else {
				fmt.Fprintf(os.Stdout, "%s  %s  id=%s\n", o.input, humanBytes(o.size), o.item.ID)
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "%s: %s: %s\n", o.input, o.code, o.message)
	}
}

type jsonSendError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type jsonSendResult struct {
	OK    bool           `json:"ok"`
	Input string         `json:"input"`
	Item  *sendcli.Item  `json:"item,omitempty"`
	Error *jsonSendError `json:"error,omitempty"`
}

// jsonResult renders the NDJSON line for one outcome.
func (o sendOutcome) jsonResult() jsonSendResult {
	if o.ok {
		return jsonSendResult{OK: true, Input: o.input, Item: o.item}
	}
	return jsonSendResult{OK: false, Input: o.input, Error: &jsonSendError{Code: o.code, Message: o.message}}
}

// sendUsage prints the send command line.
func sendUsage() {
	fmt.Fprint(os.Stderr, `usage:
  flowdrop send [--hub URL] [--json] FILE...
  flowdrop send [--hub URL] [--json] --text TEXT|- [--name NAME]
  flowdrop send [--hub URL] [--json] --link URL
  - reads stdin: files need --name, --text - reads text
`)
}
