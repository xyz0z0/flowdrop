package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"flowdrop/internal/db"
	"flowdrop/internal/pairing"
)

// isolateFlowdropEnv removes FLOWDROP_* overrides so a test config file is the
// only source of settings; the caller's values are restored afterwards.
func isolateFlowdropEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(key, "FLOWDROP_") {
			continue
		}
		old, had := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if had {
				os.Setenv(key, old)
			}
		})
	}
}

// writeTestConfig writes a config file pointing at its own throwaway data dir;
// never the user's real configuration or database.
func writeTestConfig(t *testing.T, extra string) (cfgPath, dataDir string) {
	t.Helper()
	// Neutral temp root: t.TempDir() embeds the test name ("...PasswordPrompt"),
	// which would pollute substring assertions on captured output.
	root, err := os.MkdirTemp("", "flowdrop-cli-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	dataDir = filepath.Join(root, "data")
	cfg := map[string]any{"data_dir": dataDir}
	if extra != "" {
		if err := json.Unmarshal([]byte(extra), &cfg); err != nil {
			t.Fatalf("test config: %v", err)
		}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	cfgPath = filepath.Join(root, "flowdrop.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath, dataDir
}

// captureOutput runs fn with os.Stdout/os.Stderr replaced by pipes.
func captureOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	type chunk struct{ data string }
	outC := make(chan chunk, 1)
	errC := make(chan chunk, 1)
	go func() { b, _ := io.ReadAll(outR); outC <- chunk{string(b)} }()
	go func() { b, _ := io.ReadAll(errR); errC <- chunk{string(b)} }()

	func() {
		defer func() {
			os.Stdout, os.Stderr = oldOut, oldErr
			outW.Close()
			errW.Close()
		}()
		fn()
	}()
	stdout = (<-outC).data
	stderr = (<-errC).data
	outR.Close()
	errR.Close()
	return stdout, stderr
}

var codeLine = regexp.MustCompile(`Pairing code: (\d{4})\b`)

// pairOnce runs `flowdrop pair -config FILE` and returns the printed code.
func pairOnce(t *testing.T, cfgPath string) string {
	t.Helper()
	var rc int
	stdout, stderr := captureOutput(t, func() {
		rc = run([]string{"pair", "-config", cfgPath})
	})
	if rc != 0 {
		t.Fatalf("pair exit code = %d, stderr = %q", rc, stderr)
	}
	m := codeLine.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("pair printed no 4-digit code: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("pair wrote to stderr: %q", stderr)
	}
	for _, want := range []string{"two minutes", "single use", "voids"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("pair output missing %q: %q", want, stdout)
		}
	}
	return m[1]
}

func openStore(t *testing.T, dataDir string) (*pairing.Store, func()) {
	t.Helper()
	conn, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return pairing.NewStore(conn), func() { conn.Close() }
}

func TestPairCodeIsConsumedByNewStore(t *testing.T) {
	isolateFlowdropEnv(t)
	cfgPath, dataDir := writeTestConfig(t, "")
	code := pairOnce(t, cfgPath)

	store, closeStore := openStore(t, dataDir)
	defer closeStore()
	token, device, err := store.Pair(code, "cli-test", time.Now())
	if err != nil {
		t.Fatalf("pair with CLI code: %v", err)
	}
	if len(token) == 0 || device.ID == "" {
		t.Fatalf("pair returned empty token/device: %q %q", token, device.ID)
	}
	if _, _, err := store.Pair(code, "second-use", time.Now()); !errors.Is(err, pairing.ErrInvalidCode) {
		t.Fatalf("code reused twice: err = %v, want ErrInvalidCode", err)
	}
}

func TestPairRegenerationVoidsPreviousCode(t *testing.T) {
	isolateFlowdropEnv(t)
	cfgPath, dataDir := writeTestConfig(t, "")
	first := pairOnce(t, cfgPath)
	second := pairOnce(t, cfgPath)
	if first == second {
		t.Fatalf("regenerated code %s equals the code it replaced; old value would stay usable", second)
	}

	store, closeStore := openStore(t, dataDir)
	defer closeStore()
	if _, _, err := store.Pair(first, "stale-code", time.Now()); !errors.Is(err, pairing.ErrInvalidCode) {
		t.Fatalf("old code still works: err = %v, want ErrInvalidCode", err)
	}
	if _, _, err := store.Pair(second, "fresh-code", time.Now()); err != nil {
		t.Fatalf("new code rejected: %v", err)
	}
}

func TestPairDoesNotWriteFilesOrEchoToken(t *testing.T) {
	isolateFlowdropEnv(t)
	cfgPath, dataDir := writeTestConfig(t, "")
	stdout, _ := captureOutput(t, func() {
		if rc := run([]string{"pair", "-config", cfgPath}); rc != 0 {
			t.Fatalf("pair exit code = %d", rc)
		}
	})
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if strings.Contains(name, "pair") || strings.Contains(name, "code") || strings.Contains(name, "log") {
			t.Fatalf("pair wrote an unexpected file: %s", e.Name())
		}
	}
	// A token is 32 random bytes (43 base64url chars); the output must hold the
	// 4-digit code and prose only.
	if strings.Contains(stdout, "token") || strings.Contains(stdout, "Bearer") {
		t.Fatalf("pair output mentions credentials: %q", stdout)
	}
}

func TestAdminCommandIsGone(t *testing.T) {
	isolateFlowdropEnv(t)
	cfgPath, _ := writeTestConfig(t, "")
	rc, stdout, stderr := runCaptured(t, []string{"admin", "init", "-config", cfgPath})
	if rc != 2 {
		t.Fatalf("admin exit code = %d, want 2 (unknown command)", rc)
	}
	combined := stdout + stderr
	if !strings.Contains(stderr, `unknown command "admin"`) {
		t.Fatalf("admin not reported as unknown: %q", stderr)
	}
	usage := combined[strings.Index(combined, "FlowDrop Hub"):]
	if strings.Contains(usage, "admin") || strings.Contains(usage, "password") || strings.Contains(usage, "username") {
		t.Fatalf("help still advertises the password entry point: %q", usage)
	}
	if !strings.Contains(usage, "flowdrop pair") {
		t.Fatalf("help does not advertise the pair command: %q", usage)
	}
}

func TestDoctorReportsPairingWithoutPasswordPrompt(t *testing.T) {
	isolateFlowdropEnv(t)
	// Ephemeral loopback port so the port probe cannot collide with a running
	// hub; discovery stays off so doctor touches no network service.
	cfgPath, _ := writeTestConfig(t, `{"listen":"127.0.0.1:0","discovery_enabled":false}`)
	rc, stdout, stderr := runCaptured(t, []string{"doctor", "-config", cfgPath})
	if rc != 0 {
		t.Fatalf("doctor exit code = %d, stderr = %q, stdout = %q", rc, stderr, stdout)
	}
	out := stdout + stderr
	for _, bad := range []string{"admin", "password", "admin init", "missing"} {
		if strings.Contains(strings.ToLower(out), bad) {
			t.Fatalf("doctor output still mentions %q: %q", bad, out)
		}
	}
	if !strings.Contains(out, "pairing") {
		t.Fatalf("doctor does not report pairing storage: %q", out)
	}
	if !strings.Contains(out, "0 paired") {
		t.Fatalf("doctor should treat zero paired devices as normal: %q", out)
	}
}

// runCaptured runs the CLI with stdout/stderr captured and returns the exit
// code plus both streams.
func runCaptured(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	var rc int
	stdout, stderr := captureOutput(t, func() { rc = run(args) })
	return rc, stdout, stderr
}
