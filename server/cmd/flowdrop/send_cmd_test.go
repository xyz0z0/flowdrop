package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"flowdrop/internal/api"
	"flowdrop/internal/config"
	"flowdrop/internal/db"
	"flowdrop/internal/events"
	"flowdrop/internal/pairing"
	"flowdrop/internal/sendcli"
	"flowdrop/internal/storage"
	"flowdrop/internal/store"
)

// cliHarness is a full Hub on a real listener plus an isolated config and
// credentials path, so login/send run end to end against the real handler.
type cliHarness struct {
	t         *testing.T
	root      string
	dataDir   string
	srv       *httptest.Server
	hubURL    string
	cfgPath   string
	credsPath string
	pairing   *pairing.Store
	items     *store.Store
}

func newCLIHarness(t *testing.T) *cliHarness {
	t.Helper()
	return newCLIHarnessLimit(t, 600)
}

// newCLIHarnessLimit starts the Hub with a specific API rate limit, which is
// how the rate_limited stop path is exercised deterministically.
func newCLIHarnessLimit(t *testing.T, apiLimit int) *cliHarness {
	t.Helper()
	isolateFlowdropEnv(t)
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.Listen = "127.0.0.1:0"
	cfg.CookieSecure = false
	cfg.MinFreeBytes = 0
	cfg.MaxUploadBytes = 1 << 20
	cfg.MaxConcurrentUploads = 2
	cfg.PairLimitPerMinute = 1000
	cfg.APILimitPerMinute = apiLimit
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validate: %v", err)
	}

	conn, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	files, err := storage.New(dataDir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	pstore := pairing.NewStore(conn)
	st := store.New(conn)
	apiSrv := api.New(api.Deps{
		Config:  cfg,
		Pairing: pstore,
		Store:   st,
		Storage: files,
		Broker:  events.NewBroker(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(ts.Close)

	cfgPath := filepath.Join(root, "flowdrop.json")
	body, err := json.Marshal(map[string]any{
		"data_dir":               dataDir,
		"listen":                 "127.0.0.1:0",
		"cookie_secure":          false,
		"min_free_bytes":         0,
		"max_upload_bytes":       1 << 20,
		"max_concurrent_uploads": 2,
		"pair_limit_per_minute":  1000,
		"api_limit_per_minute":   apiLimit,
		"discovery_enabled":      false,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	credsPath := filepath.Join(root, "cli.json")
	// Keep the default credentials path inside the temp dir as well.
	t.Setenv("FLOWDROP_CLI_CONFIG", credsPath)

	return &cliHarness{
		t:         t,
		root:      root,
		dataDir:   dataDir,
		srv:       ts,
		hubURL:    ts.URL,
		cfgPath:   cfgPath,
		credsPath: credsPath,
		pairing:   pstore,
		items:     st,
	}
}

// code creates a fresh pairing code the way the desktop settings do.
func (h *cliHarness) code() string {
	h.t.Helper()
	c, err := h.pairing.CreateCode(time.Now())
	if err != nil {
		h.t.Fatalf("create pairing code: %v", err)
	}
	return c.Code
}

// login runs `flowdrop login` against this Hub's config.
func (h *cliHarness) login(extra ...string) (int, string, string) {
	h.t.Helper()
	args := []string{"login", "-config", h.cfgPath, "--hub", h.hubURL, "--credentials", h.credsPath}
	args = append(args, extra...)
	return runCaptured(h.t, args)
}

// send runs `flowdrop send` with this harness's config file.
func (h *cliHarness) send(args ...string) (int, string, string) {
	h.t.Helper()
	full := append([]string{"send", "-config", h.cfgPath}, args...)
	return runCaptured(h.t, full)
}

// writeFile creates a file with content and returns its path.
func (h *cliHarness) writeFile(name string, content []byte) string {
	h.t.Helper()
	p := filepath.Join(h.root, name)
	if err := os.WriteFile(p, content, 0o600); err != nil {
		h.t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// mustLogin logs in and returns the saved credentials.
func (h *cliHarness) mustLogin() sendcli.Credentials {
	h.t.Helper()
	rc, stdout, stderr := h.login("--code", h.code(), "--name", "cli-unit")
	if rc != 0 {
		h.t.Fatalf("login exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	creds, err := sendcli.LoadCredentials(h.credsPath)
	if err != nil {
		h.t.Fatalf("load credentials: %v", err)
	}
	return creds
}

// setStdin replaces os.Stdin with content for the rest of the test.
func setStdin(t *testing.T, content []byte) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatalf("stdin temp: %v", err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek stdin: %v", err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = old
		f.Close()
	})
}

// listItems returns all stored items (newest first).
func (h *cliHarness) listItems() []store.Item {
	h.t.Helper()
	items, _, err := h.items.ListItems(nil, 100)
	if err != nil {
		h.t.Fatalf("list items: %v", err)
	}
	return items
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var idLine = regexp.MustCompile(`id=(\S+)`)

func TestLoginSavesCredentialsWithoutEchoingSecrets(t *testing.T) {
	h := newCLIHarness(t)
	code := h.code()

	rc, stdout, stderr := h.login("--code", code, "--name", "cli-laptop")
	if rc != 0 {
		t.Fatalf("login exit code = %d, stderr = %q", rc, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("login wrote to stderr: %q", stderr)
	}

	info, err := os.Stat(h.credsPath)
	if err != nil {
		t.Fatalf("stat credentials: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credentials mode = %04o, want 0600", perm)
	}

	creds, err := sendcli.LoadCredentials(h.credsPath)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if creds.DeviceName != "cli-laptop" || creds.DeviceID == "" || creds.DeviceToken == "" {
		t.Fatalf("unexpected credentials: %+v", creds)
	}
	for _, want := range []string{h.hubURL, "cli-laptop", creds.DeviceID, h.credsPath, "revoke"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("login output missing %q: %q", want, stdout)
		}
	}
	if strings.Contains(stdout, creds.DeviceToken) {
		t.Fatalf("login echoed the device token: %q", stdout)
	}
	if strings.Contains(stdout, code) {
		t.Fatalf("login echoed the pairing code: %q", stdout)
	}

	// The saved file is immediately usable for a send.
	path := h.writeFile("report.txt", []byte("hello flowdrop"))
	rc, out, errOut := h.send("--credentials", h.credsPath, path)
	if rc != 0 {
		t.Fatalf("send exit code = %d, stdout = %q, stderr = %q", rc, out, errOut)
	}
	if !idLine.MatchString(out) {
		t.Fatalf("send output has no id: %q", out)
	}
}

func TestLoginGeneratesPairingCodeLocally(t *testing.T) {
	h := newCLIHarness(t)
	// No --code: the CLI inserts the code into the local database itself.
	rc, stdout, stderr := h.login("--name", "auto-code")
	if rc != 0 {
		t.Fatalf("login exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	if _, err := sendcli.LoadCredentials(h.credsPath); err != nil {
		t.Fatalf("credentials not saved: %v", err)
	}
	// The generated code must never be printed.
	if strings.Contains(strings.ToLower(stdout), "code:") || strings.Contains(stdout, "Pairing code") {
		t.Fatalf("login printed a pairing code: %q", stdout)
	}
	devices, err := h.pairing.ListDevices()
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "auto-code" {
		t.Fatalf("devices = %+v, want one device named auto-code", devices)
	}
}

func TestLoginWithoutLocalCodeFails(t *testing.T) {
	h := newCLIHarness(t)
	// data_dir under a regular file: the local database cannot be opened.
	blocked := filepath.Join(h.root, "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocked file: %v", err)
	}
	cfgPath := filepath.Join(h.root, "broken.json")
	body, _ := json.Marshal(map[string]any{"data_dir": filepath.Join(blocked, "data")})
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatalf("write broken config: %v", err)
	}

	rc, _, stderr := runCaptured(t, []string{"login", "-config", cfgPath, "--hub", h.hubURL, "--credentials", h.credsPath})
	if rc != 1 {
		t.Fatalf("login exit code = %d, want 1; stderr = %q", rc, stderr)
	}
	if !strings.Contains(stderr, "--code") {
		t.Fatalf("login does not suggest --code: %q", stderr)
	}
	if _, err := os.Stat(h.credsPath); !os.IsNotExist(err) {
		t.Fatalf("failed login wrote a credentials file (err = %v)", err)
	}
}

func TestSendFileReportsDigestAndItem(t *testing.T) {
	h := newCLIHarness(t)
	creds := h.mustLogin()
	content := []byte("flowdrop send payload\n")
	path := h.writeFile("report.txt", content)

	rc, stdout, stderr := h.send("--credentials", h.credsPath, path)
	if rc != 0 {
		t.Fatalf("send exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	m := idLine.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("send output has no id: %q", stdout)
	}
	id := m[1]
	want := sha256Hex(content)
	if !strings.Contains(stdout, "sha256="+want) {
		t.Fatalf("send output missing sha256 %s: %q", want, stdout)
	}

	item, data := h.getItem(creds.DeviceToken, id)
	if got := string(data); got != string(content) {
		t.Fatalf("stored content = %q, want %q", got, content)
	}
	blob := item["blob"].(map[string]any)
	if blob["sha256"] != want {
		t.Fatalf("stored sha256 = %v, want %s", blob["sha256"], want)
	}
	if int64(blob["size_bytes"].(float64)) != int64(len(content)) {
		t.Fatalf("stored size = %v, want %d", blob["size_bytes"], len(content))
	}
}

// getItemMeta fetches one item's metadata over an independent authenticated
// request. It does not download the blob, so it also works for text items.
func (h *cliHarness) getItemMeta(token, id string) map[string]any {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.hubURL+"/api/v1/items/"+id, nil)
	if err != nil {
		h.t.Fatalf("build get item: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("get item: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("get item status %d: %s", resp.StatusCode, body)
	}
	var item map[string]any
	if err := json.Unmarshal(body, &item); err != nil {
		h.t.Fatalf("decode item: %v", err)
	}
	return item
}

// getItem fetches an item and its content over independent authenticated
// requests.
func (h *cliHarness) getItem(token, id string) (map[string]any, []byte) {
	h.t.Helper()
	item := h.getItemMeta(token, id)

	dreq, err := http.NewRequest(http.MethodGet, h.hubURL+"/api/v1/items/"+id+"/download", nil)
	if err != nil {
		h.t.Fatalf("build download: %v", err)
	}
	dreq.Header.Set("Authorization", "Bearer "+token)
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		h.t.Fatalf("download: %v", err)
	}
	defer dresp.Body.Close()
	stored, _ := io.ReadAll(io.LimitReader(dresp.Body, 1<<20))
	if dresp.StatusCode != http.StatusOK {
		h.t.Fatalf("download status %d", dresp.StatusCode)
	}
	return item, stored
}

// TestSendCarriesPairedDeviceName covers the display label the Web inbox shows:
// after `login --name cli-laptop` both a file and a text item carry that name.
func TestSendCarriesPairedDeviceName(t *testing.T) {
	h := newCLIHarness(t)
	rc, stdout, stderr := h.login("--code", h.code(), "--name", "cli-laptop")
	if rc != 0 {
		t.Fatalf("login exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	creds, err := sendcli.LoadCredentials(h.credsPath)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}

	path := h.writeFile("label.txt", []byte("labelled payload"))
	rc, stdout, stderr = h.send("--credentials", h.credsPath, path)
	if rc != 0 {
		t.Fatalf("file send exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	m := idLine.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("file send output has no id: %q", stdout)
	}
	fileItem, data := h.getItem(creds.DeviceToken, m[1])
	if fileItem["sender_device"] != "cli-laptop" {
		t.Fatalf("file sender_device = %v, want cli-laptop", fileItem["sender_device"])
	}
	if string(data) != "labelled payload" {
		t.Fatalf("stored content = %q", data)
	}

	rc, stdout, stderr = h.send("--text", "hello label", "--credentials", h.credsPath)
	if rc != 0 {
		t.Fatalf("text send exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	m = idLine.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("text send output has no id: %q", stdout)
	}
	textItem := h.getItemMeta(creds.DeviceToken, m[1])
	if textItem["sender_device"] != "cli-laptop" {
		t.Fatalf("text sender_device = %v, want cli-laptop", textItem["sender_device"])
	}
	if textItem["text"] != "hello label" {
		t.Fatalf("stored text = %v", textItem["text"])
	}

	// --name still only labels stdin files and never becomes the sender.
	setStdin(t, []byte("piped label"))
	rc, stdout, stderr = h.send("--name", "piped.txt", "--credentials", h.credsPath, "-")
	if rc != 0 {
		t.Fatalf("stdin send exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	m = idLine.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("stdin send output has no id: %q", stdout)
	}
	stdinItem, _ := h.getItem(creds.DeviceToken, m[1])
	if stdinItem["sender_device"] != "cli-laptop" {
		t.Fatalf("stdin sender_device = %v, want cli-laptop (not the --name file name)", stdinItem["sender_device"])
	}
	blob := stdinItem["blob"].(map[string]any)
	if blob["name"] != "piped.txt" {
		t.Fatalf("stored name = %v, want piped.txt", blob["name"])
	}
}

func TestSendJSONIsNDJSONInInputOrder(t *testing.T) {
	h := newCLIHarness(t)
	h.mustLogin()
	first := h.writeFile("a.bin", []byte("alpha"))
	second := h.writeFile("b.bin", []byte("bravo"))

	rc, stdout, stderr := h.send("--json", "--credentials", h.credsPath, first, second)
	if rc != 0 {
		t.Fatalf("send exit code = %d, stderr = %q", rc, stderr)
	}
	lines := nonEmptyLines(stdout)
	if len(lines) != 2 {
		t.Fatalf("got %d NDJSON lines, want 2: %q", len(lines), stdout)
	}
	for i, want := range []struct {
		input   string
		content []byte
	}{{first, []byte("alpha")}, {second, []byte("bravo")}} {
		var got struct {
			OK    bool   `json:"ok"`
			Input string `json:"input"`
			Item  struct {
				ID   string `json:"id"`
				Blob struct {
					SHA256 string `json:"sha256"`
				} `json:"blob"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &got); err != nil {
			t.Fatalf("line %d is not JSON: %q", i, lines[i])
		}
		if !got.OK || got.Input != want.input || got.Item.ID == "" {
			t.Fatalf("line %d = %s, want ok item for %s", i, lines[i], want.input)
		}
		if sha := sha256Hex(want.content); got.Item.Blob.SHA256 != sha {
			t.Fatalf("line %d sha256 = %q, want %s", i, got.Item.Blob.SHA256, sha)
		}
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func TestSendTextFromStdinAndLimit(t *testing.T) {
	h := newCLIHarness(t)
	h.mustLogin()

	setStdin(t, []byte("hello from stdin"))
	rc, stdout, stderr := h.send("--text", "-", "--credentials", h.credsPath)
	if rc != 0 {
		t.Fatalf("send --text - exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	if !strings.Contains(stdout, "id=") {
		t.Fatalf("text send output has no id: %q", stdout)
	}
	items := h.listItems()
	if len(items) != 1 || items[0].Type != "text" || items[0].Text != "hello from stdin" {
		t.Fatalf("stored items = %+v, want one text item", items)
	}

	// Oversized stdin: rejected locally, no new server item.
	before := len(items)
	setStdin(t, bytes.Repeat([]byte("x"), maxTextBytes+1))
	rc, _, stderr = h.send("--text", "-", "--credentials", h.credsPath)
	if rc != 1 {
		t.Fatalf("oversized text exit code = %d, want 1; stderr = %q", rc, stderr)
	}
	if !strings.Contains(stderr, "text_too_large") {
		t.Fatalf("oversized text error = %q, want text_too_large", stderr)
	}
	if after := len(h.listItems()); after != before {
		t.Fatalf("oversized text created items: %d -> %d", before, after)
	}
}

func TestSendLink(t *testing.T) {
	h := newCLIHarness(t)
	h.mustLogin()

	rc, stdout, stderr := h.send("--link", "https://example.com", "--credentials", h.credsPath)
	if rc != 0 {
		t.Fatalf("link send exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	if !strings.Contains(stdout, "id=") {
		t.Fatalf("link output has no id: %q", stdout)
	}

	rc, stdout, stderr = h.send("--link", "ftp://x", "--credentials", h.credsPath)
	if rc != 1 {
		t.Fatalf("invalid link exit code = %d, want 1", rc)
	}
	if !strings.Contains(stderr, "invalid_link") && !strings.Contains(stdout, "invalid_link") {
		t.Fatalf("invalid link not reported: stdout = %q, stderr = %q", stdout, stderr)
	}

	// The same failure is reported as NDJSON in --json mode.
	rc, stdout, stderr = h.send("--json", "--link", "ftp://x", "--credentials", h.credsPath)
	if rc != 1 {
		t.Fatalf("invalid link --json exit code = %d, want 1; stderr = %q", rc, stderr)
	}
	lines := nonEmptyLines(stdout)
	if len(lines) != 1 || !strings.Contains(lines[0], `"invalid_link"`) {
		t.Fatalf("invalid link NDJSON = %q", stdout)
	}
	var got struct {
		OK    bool   `json:"ok"`
		Input string `json:"input"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("invalid link line is not JSON: %q", lines[0])
	}
	if got.OK || got.Input != "link" || got.Error.Code != "invalid_link" {
		t.Fatalf("invalid link NDJSON = %s", lines[0])
	}
}

func TestSendDirectoryFailsButNextFileSends(t *testing.T) {
	h := newCLIHarness(t)
	h.mustLogin()
	dir := filepath.Join(h.root, "somedir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := h.writeFile("ok.txt", []byte("still sent"))

	rc, stdout, stderr := h.send("--credentials", h.credsPath, dir, file)
	if rc != 1 {
		t.Fatalf("exit code = %d, want 1; stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	if !strings.Contains(stderr, dir) || !strings.Contains(stderr, "not_regular_file") {
		t.Fatalf("directory failure not reported: %q", stderr)
	}
	if !strings.Contains(stdout, file) || !strings.Contains(stdout, "id=") {
		t.Fatalf("following file was not sent: %q", stdout)
	}
	if len(h.listItems()) != 1 {
		t.Fatalf("items = %d, want exactly the one file", len(h.listItems()))
	}
}

func TestSendWithoutLoginFails(t *testing.T) {
	h := newCLIHarness(t)
	path := h.writeFile("x.txt", []byte("x"))

	rc, stdout, stderr := h.send("--credentials", h.credsPath, path)
	if rc != 1 {
		t.Fatalf("exit code = %d, want 1; stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	if !strings.Contains(stderr, "flowdrop login") {
		t.Fatalf("error does not suggest flowdrop login: %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("missing credentials wrote to stdout: %q", stdout)
	}
}

func TestSendUsageErrors(t *testing.T) {
	h := newCLIHarness(t)
	h.mustLogin()
	file := h.writeFile("u.txt", []byte("u"))

	cases := []struct {
		name string
		args []string
	}{
		{"text and file", []string{"--text", "hi", "--credentials", h.credsPath, file}},
		{"text and link", []string{"--text", "hi", "--link", "https://example.com", "--credentials", h.credsPath}},
		{"stdin without name", []string{"--credentials", h.credsPath, "-"}},
		{"name with real file", []string{"--name", "x.txt", "--credentials", h.credsPath, file}},
		{"no input", []string{"--credentials", h.credsPath}},
	}
	for _, tc := range cases {
		rc, _, stderr := h.send(tc.args...)
		if rc != 2 {
			t.Fatalf("%s: exit code = %d, want 2; stderr = %q", tc.name, rc, stderr)
		}
		if !strings.Contains(stderr, "flowdrop send") {
			t.Fatalf("%s: no usage on stderr: %q", tc.name, stderr)
		}
	}
}

// TestSendStopsOnRateLimit covers the rate-limited stop: the failing input is
// reported, the rest are skipped unsent, and the exit code is 1.
func TestSendStopsOnRateLimit(t *testing.T) {
	h := newCLIHarnessLimit(t, 2) // two uploads fit, the third is rate limited
	h.mustLogin()
	var paths []string
	for _, name := range []string{"one.txt", "two.txt", "three.txt", "four.txt"} {
		paths = append(paths, h.writeFile(name, []byte(name)))
	}

	rc, stdout, stderr := h.send(append([]string{"--json", "--credentials", h.credsPath}, paths...)...)
	if rc != 1 {
		t.Fatalf("exit code = %d, want 1; stderr = %q", rc, stderr)
	}
	lines := nonEmptyLines(stdout)
	if len(lines) != 4 {
		t.Fatalf("got %d NDJSON lines, want 4: %q", len(lines), stdout)
	}
	var last struct {
		OK    bool   `json:"ok"`
		Input string `json:"input"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil {
		t.Fatalf("third line is not JSON: %q", lines[2])
	}
	if last.OK || last.Error.Code != "rate_limited" {
		t.Fatalf("third line = %s, want rate_limited failure", lines[2])
	}
	if err := json.Unmarshal([]byte(lines[3]), &last); err != nil {
		t.Fatalf("fourth line is not JSON: %q", lines[3])
	}
	if last.OK || last.Error.Code != "skipped" || last.Input != paths[3] {
		t.Fatalf("fourth line = %s, want skipped for %s", lines[3], paths[3])
	}
}

// TestSendStdinFileUsesName covers the spooled stdin file mode.
func TestSendStdinFileUsesName(t *testing.T) {
	h := newCLIHarness(t)
	creds := h.mustLogin()
	content := []byte("piped content")
	setStdin(t, content)

	rc, stdout, stderr := h.send("--name", "piped.txt", "--credentials", h.credsPath, "-")
	if rc != 0 {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	m := idLine.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("stdin upload output has no id: %q", stdout)
	}
	item, data := h.getItem(creds.DeviceToken, m[1])
	blob := item["blob"].(map[string]any)
	if blob["name"] != "piped.txt" {
		t.Fatalf("stored name = %v, want piped.txt", blob["name"])
	}
	if fmt.Sprintf("%s", data) != string(content) {
		t.Fatalf("stored content = %q, want %q", data, content)
	}
}

// TestSendUsesEnvTokenInsteadOfFile covers FLOWDROP_TOKEN + FLOWDROP_HUB
// winning over the saved credentials without writing anything to disk.
func TestSendUsesEnvTokenInsteadOfFile(t *testing.T) {
	h := newCLIHarness(t)
	creds := h.mustLogin()
	path := h.writeFile("env.txt", []byte("env token"))
	if err := os.Remove(h.credsPath); err != nil {
		t.Fatalf("remove credentials: %v", err)
	}
	t.Setenv("FLOWDROP_HUB", h.hubURL)
	t.Setenv("FLOWDROP_TOKEN", creds.DeviceToken)

	rc, stdout, stderr := h.send(path)
	if rc != 0 {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	if !strings.Contains(stdout, "id=") {
		t.Fatalf("output has no id: %q", stdout)
	}
	if _, err := os.Stat(h.credsPath); !os.IsNotExist(err) {
		t.Fatalf("env-token send wrote a credentials file (err = %v)", err)
	}
	// There is no device name on the env path, so the default cli-<hostname>
	// label is used.
	m := idLine.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("output has no id: %q", stdout)
	}
	sender, _ := h.getItem(creds.DeviceToken, m[1])
	got, _ := sender["sender_device"].(string)
	if got == "" || !strings.HasPrefix(got, "cli-") {
		t.Fatalf("env-token sender_device = %q, want a cli- default label", got)
	}
}

// TestSendReportsRevokedDevice covers Connect returning ErrUnauthorized.
func TestSendReportsRevokedDevice(t *testing.T) {
	h := newCLIHarness(t)
	creds := h.mustLogin()
	if err := h.pairing.RevokeDevice(creds.DeviceID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	path := h.writeFile("revoked.txt", []byte("x"))

	rc, stdout, stderr := h.send("--credentials", h.credsPath, path)
	if rc != 1 {
		t.Fatalf("exit code = %d, want 1; stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	if !strings.Contains(stderr, "flowdrop login") {
		t.Fatalf("revoked token error does not suggest login: %q", stderr)
	}
	if strings.Contains(stdout, `"ok":true`) {
		t.Fatalf("revoked token reported success: %q", stdout)
	}
	if len(h.listItems()) != 0 {
		t.Fatalf("revoked token created items: %d", len(h.listItems()))
	}
}

// TestLoginDerivesHubFromConfig logs in without --hub: the hub URL comes from
// the first listen address of the local config file.
func TestLoginDerivesHubFromConfig(t *testing.T) {
	h := newCLIHarness(t)
	port := h.hubURL[strings.LastIndex(h.hubURL, ":")+1:]
	cfgPath := filepath.Join(h.root, "derived.json")
	body, err := json.Marshal(map[string]any{
		"data_dir":               h.dataDir,
		"listen":                 "127.0.0.1:" + port,
		"cookie_secure":          false,
		"min_free_bytes":         0,
		"max_upload_bytes":       1 << 20,
		"max_concurrent_uploads": 2,
		"pair_limit_per_minute":  1000,
		"discovery_enabled":      false,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	credsPath := filepath.Join(h.root, "derived-cli.json")

	rc, stdout, stderr := runCaptured(t, []string{
		"login", "-config", cfgPath, "--code", h.code(),
		"--credentials", credsPath, "--name", "derived",
	})
	if rc != 0 {
		t.Fatalf("login exit code = %d, stdout = %q, stderr = %q", rc, stdout, stderr)
	}
	want := "http://127.0.0.1:" + port
	if !strings.Contains(stdout, want) {
		t.Fatalf("derived hub URL %q missing from output: %q", want, stdout)
	}
	if _, err := sendcli.LoadCredentials(credsPath); err != nil {
		t.Fatalf("credentials not saved: %v", err)
	}
}

// TestResolveHubURL covers the precedence and wildcard/scheme rules.
func TestResolveHubURL(t *testing.T) {
	isolateFlowdropEnv(t)

	cfg := config.Default()
	cfg.Listen = "0.0.0.0:8080"
	cfg.AdditionalListen = nil
	cfg.CookieSecure = false
	got, err := resolveHubURL("", cfg)
	if err != nil {
		t.Fatalf("resolveHubURL: %v", err)
	}
	if want := "http://127.0.0.1:8080"; got != want {
		t.Fatalf("wildcard listen = %q, want %q", got, want)
	}

	cfg.CookieSecure = true
	got, err = resolveHubURL("", cfg)
	if err != nil {
		t.Fatalf("resolveHubURL: %v", err)
	}
	if want := "https://127.0.0.1:8080"; got != want {
		t.Fatalf("secure listen = %q, want %q", got, want)
	}

	cfg.Listen = "[::]:9000"
	cfg.CookieSecure = false
	if got, err = resolveHubURL("", cfg); err != nil || got != "http://127.0.0.1:9000" {
		t.Fatalf("ipv6 wildcard = %q (err %v), want http://127.0.0.1:9000", got, err)
	}

	// FLOWDROP_HUB wins over the config, --hub wins over the environment.
	t.Setenv("FLOWDROP_HUB", "https://env.example")
	if got, err = resolveHubURL("", cfg); err != nil || got != "https://env.example" {
		t.Fatalf("env hub = %q (err %v), want https://env.example", got, err)
	}
	if got, err = resolveHubURL("http://flag.example:1234", cfg); err != nil || got != "http://flag.example:1234" {
		t.Fatalf("flag hub = %q (err %v), want http://flag.example:1234", got, err)
	}

	// No source at all reports the example command.
	os.Unsetenv("FLOWDROP_HUB")
	cfg.Listen = "not-a-host-port"
	if _, err = resolveHubURL("", cfg); err == nil || !strings.Contains(err.Error(), "--hub") {
		t.Fatalf("missing hub error = %v, want a --hub hint", err)
	}
}
