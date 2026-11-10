// End-to-end tests for the CLI client. Every call goes through the real Hub
// handler (router, auth, CSRF, multipart and store), only the data directory is
// a temp dir, so no user state is touched.
package sendcli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

type harness struct {
	t       *testing.T
	srv     *httptest.Server
	hubURL  string
	pairing *pairing.Store
}

// newHarness starts a full Hub on a real listener with a temp data dir.
func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Listen = "127.0.0.1:0"
	cfg.CookieSecure = false
	cfg.MinFreeBytes = 0
	cfg.MaxUploadBytes = 1 << 20
	cfg.MaxConcurrentUploads = 2
	cfg.PairLimitPerMinute = 1000

	conn, err := db.Open(cfg.DataDir)
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	files, err := storage.New(cfg.DataDir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	pstore := pairing.NewStore(conn)
	apiSrv := api.New(api.Deps{
		Config:  cfg,
		Pairing: pstore,
		Store:   store.New(conn),
		Storage: files,
		Broker:  events.NewBroker(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(ts.Close)

	return &harness{t: t, srv: ts, hubURL: ts.URL, pairing: pstore}
}

// code generates a fresh pairing code the way the desktop does.
func (h *harness) code() string {
	h.t.Helper()
	c, err := h.pairing.CreateCode(time.Now())
	if err != nil {
		h.t.Fatalf("create pairing code: %v", err)
	}
	return c.Code
}

// paired returns credentials for a device on this Hub.
func (h *harness) paired(name string) sendcli.Credentials {
	h.t.Helper()
	creds, err := sendcli.Pair(context.Background(), h.hubURL, h.code(), name)
	if err != nil {
		h.t.Fatalf("pair: %v", err)
	}
	return creds
}

// connected returns a client that already completed Connect.
func (h *harness) connected() *sendcli.Client {
	h.t.Helper()
	creds := h.paired("cli-test")
	c, err := sendcli.NewClient(creds.HubURL, creds.DeviceToken)
	if err != nil {
		h.t.Fatalf("new client: %v", err)
	}
	if err := c.Connect(context.Background()); err != nil {
		h.t.Fatalf("connect: %v", err)
	}
	return c
}

// getItem fetches an item over an independent authenticated request (Bearer
// device token, no CSRF needed for reads).
func (h *harness) getItem(token, id string) (map[string]any, []byte) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.hubURL+"/api/v1/items/"+id, nil)
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

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, h.hubURL+"/api/v1/items/"+id+"/download", nil)
	if err != nil {
		h.t.Fatalf("build download: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	dresp, err := http.DefaultClient.Do(req)
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

// writeTemp creates a temp file with deterministic content.
func writeTemp(t *testing.T, name string, size int) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i%251 + 1)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return p, data
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestPairSavesAndLoadsCredentials(t *testing.T) {
	h := newHarness(t)
	code := h.code()

	creds, err := sendcli.Pair(context.Background(), h.hubURL+"/", code, "  cli-laptop  ")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if creds.DeviceToken == "" || creds.DeviceID == "" {
		t.Fatalf("pair returned incomplete credentials: %+v", creds)
	}
	if creds.DeviceName != "cli-laptop" {
		t.Fatalf("device name = %q, want trimmed cli-laptop", creds.DeviceName)
	}
	// The trailing slash of the input is normalized away.
	if creds.HubURL != h.hubURL {
		t.Fatalf("hub url = %q, want %q", creds.HubURL, h.hubURL)
	}

	path := filepath.Join(t.TempDir(), "cfg", "cli.json")
	if err := sendcli.SaveCredentials(path, creds); err != nil {
		t.Fatalf("save credentials: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credentials: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credentials mode = %04o, want 0600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config dir mode = %04o, want 0700", perm)
	}

	// The file holds exactly the documented fields and nothing else.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode credentials file: %v", err)
	}
	want := []string{"hub_url", "device_token", "device_id", "device_name", "created_at"}
	if len(fields) != len(want) {
		t.Fatalf("credentials fields = %v, want exactly %v", fields, want)
	}
	for _, k := range want {
		if _, ok := fields[k]; !ok {
			t.Fatalf("credentials file is missing %q: %s", k, raw)
		}
	}
	if got := string(bytes.TrimSpace(fields["device_token"])); got != `"`+creds.DeviceToken+`"` {
		t.Fatalf("stored device token does not match paired token")
	}

	loaded, err := sendcli.LoadCredentials(path)
	if err != nil {
		t.Fatalf("load credentials: %v", err)
	}
	if loaded != creds {
		t.Fatalf("round-trip mismatch: loaded %+v, want %+v", loaded, creds)
	}
}

func TestPairRejectsInvalidCode(t *testing.T) {
	h := newHarness(t)
	code := h.code()
	wrong := "0000"
	if code == wrong {
		wrong = "0001"
	}

	_, err := sendcli.Pair(context.Background(), h.hubURL, wrong, "cli-test")
	if err == nil {
		t.Fatal("pair with a wrong code succeeded")
	}
	var apiErr *sendcli.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T (%v), want *sendcli.Error", err, err)
	}
	if apiErr.Code != "pairing_invalid" || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("error = %+v, want 401 pairing_invalid", apiErr)
	}
}

func TestPairRejectsEmptyDeviceNameWithoutRequest(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := sendcli.Pair(context.Background(), srv.URL, "1234", "   "); err == nil {
		t.Fatal("pair with an empty device name succeeded")
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("pair sent %d request(s) despite an empty device name", n)
	}
}

func TestLoadCredentialsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "cli.json")
	if _, err := sendcli.LoadCredentials(path); !errors.Is(err, sendcli.ErrNoCredentials) {
		t.Fatalf("load missing credentials error = %v, want ErrNoCredentials", err)
	}

	// A file without hub_url/device_token is not a usable login either.
	partial := filepath.Join(t.TempDir(), "cli.json")
	if err := os.WriteFile(partial, []byte(`{"hub_url":"http://127.0.0.1:1","device_token":""}`), 0o600); err != nil {
		t.Fatalf("write partial credentials: %v", err)
	}
	if _, err := sendcli.LoadCredentials(partial); !errors.Is(err, sendcli.ErrNoCredentials) {
		t.Fatalf("load incomplete credentials error = %v, want ErrNoCredentials", err)
	}
}

func TestDefaultCredentialsPath(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "custom", "creds.json")
	t.Setenv("FLOWDROP_CLI_CONFIG", custom)
	if got := sendcli.DefaultCredentialsPath(); got != custom {
		t.Fatalf("path = %q, want %q", got, custom)
	}

	t.Setenv("FLOWDROP_CLI_CONFIG", "")
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("no user config dir on this platform: %v", err)
	}
	want := filepath.Join(dir, "flowdrop", "cli.json")
	if got := sendcli.DefaultCredentialsPath(); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestConnectAndLimits(t *testing.T) {
	h := newHarness(t)
	creds := h.paired("cli-test")

	c, err := sendcli.NewClient(creds.HubURL, creds.DeviceToken)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if got := c.MaxUploadBytes(); got != 0 {
		t.Fatalf("MaxUploadBytes before Connect = %d, want 0", got)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if got := c.MaxUploadBytes(); got != 1<<20 {
		t.Fatalf("MaxUploadBytes = %d, want %d", got, 1<<20)
	}
}

func TestConnectRejectsForgedToken(t *testing.T) {
	h := newHarness(t)
	c, err := sendcli.NewClient(h.hubURL, "forged-token-not-issued-by-this-hub")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	err = c.Connect(context.Background())
	if !errors.Is(err, sendcli.ErrUnauthorized) {
		t.Fatalf("connect with a forged token error = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), "forged-token") {
		t.Fatalf("error leaks the device token: %v", err)
	}
}

func TestSendFileUploadsRealBytes(t *testing.T) {
	h := newHarness(t)
	creds := h.paired("cli-test")
	c, err := sendcli.NewClient(creds.HubURL, creds.DeviceToken)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}

	path, data := writeTemp(t, "report.txt", 4096)
	item, err := c.SendFile(context.Background(), path)
	if err != nil {
		t.Fatalf("send file: %v", err)
	}
	if item.ID == "" {
		t.Fatal("upload returned no item id")
	}
	if item.Type != "file" {
		t.Fatalf("item type = %q, want file", item.Type)
	}
	if item.Blob == nil {
		t.Fatal("upload returned no blob")
	}
	if item.Blob.SHA256 != sha256Hex(data) {
		t.Fatalf("blob sha256 = %q, want %q", item.Blob.SHA256, sha256Hex(data))
	}
	if item.Blob.SizeBytes != int64(len(data)) {
		t.Fatalf("blob size = %d, want %d", item.Blob.SizeBytes, len(data))
	}
	if item.Blob.OriginalName != "report.txt" {
		t.Fatalf("blob name = %q, want report.txt", item.Blob.OriginalName)
	}

	// The Hub really stored the item: an independent authenticated read agrees.
	stored, body := h.getItem(creds.DeviceToken, item.ID)
	if stored["id"] != item.ID || stored["type"] != "file" {
		t.Fatalf("stored item = %v", stored)
	}
	blob, ok := stored["blob"].(map[string]any)
	if !ok {
		t.Fatalf("stored item has no blob: %v", stored)
	}
	if blob["sha256"] != item.Blob.SHA256 {
		t.Fatalf("stored sha256 = %v, want %v", blob["sha256"], item.Blob.SHA256)
	}
	if blob["size_bytes"] != float64(len(data)) {
		t.Fatalf("stored size = %v, want %d", blob["size_bytes"], len(data))
	}
	if !bytes.Equal(body, data) {
		t.Fatalf("downloaded %d bytes, want the %d uploaded bytes", len(body), len(data))
	}
}

func TestSendFileRejectsNonRegularFile(t *testing.T) {
	c := newHarness(t).connected()
	if _, err := c.SendFile(context.Background(), t.TempDir()); err == nil {
		t.Fatal("sending a directory succeeded")
	}
}

func TestSendTextAndLink(t *testing.T) {
	c := newHarness(t).connected()
	ctx := context.Background()

	item, err := c.SendText(ctx, "text", "hello from the cli\n\n")
	if err != nil {
		t.Fatalf("send text: %v", err)
	}
	if item.Type != "text" || item.Text != "hello from the cli" {
		t.Fatalf("text item = %+v", item)
	}

	link, err := c.SendText(ctx, "link", "https://example.com")
	if err != nil {
		t.Fatalf("send link: %v", err)
	}
	if link.Type != "link" || link.Text != "https://example.com" {
		t.Fatalf("link item = %+v", link)
	}

	_, err = c.SendText(ctx, "link", "ftp://x")
	var apiErr *sendcli.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("invalid link error = %v (%T), want *sendcli.Error", err, err)
	}
	if apiErr.Code != "invalid_link" || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("invalid link error = %+v, want 400 invalid_link", apiErr)
	}
}

func TestSendFileTooLarge(t *testing.T) {
	c := newHarness(t).connected()
	path, _ := writeTemp(t, "big.bin", (1<<20)+1)

	_, err := c.SendFile(context.Background(), path)
	var apiErr *sendcli.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("oversized upload error = %v (%T), want *sendcli.Error", err, err)
	}
	if apiErr.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload status = %d, want 413 (%v)", apiErr.Status, apiErr)
	}
}

func TestSendBeforeConnectFails(t *testing.T) {
	h := newHarness(t)
	creds := h.paired("cli-test")
	c, err := sendcli.NewClient(creds.HubURL, creds.DeviceToken)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	path, _ := writeTemp(t, "before.txt", 16)
	_, err = c.SendFile(context.Background(), path)
	if err == nil {
		t.Fatal("SendFile before Connect succeeded")
	}
	var apiErr *sendcli.Error
	if errors.As(err, &apiErr) {
		t.Fatalf("SendFile before Connect returned a Hub error instead of a local one: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "connect") {
		t.Fatalf("error does not explain that Connect is required: %v", err)
	}
	if apiErr != nil && apiErr.Status == http.StatusForbidden {
		t.Fatalf("SendFile before Connect returned 403")
	}

	if _, err := c.SendText(context.Background(), "text", "hi"); err == nil {
		t.Fatal("SendText before Connect succeeded")
	}
	if _, err := c.SendReader(context.Background(), "x.bin", -1, strings.NewReader("x")); err == nil {
		t.Fatal("SendReader accepted a negative size")
	}
}

func TestNewClientRejectsBadURL(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com", "http://", "not a url"} {
		if _, err := sendcli.NewClient(raw, "token"); err == nil {
			t.Fatalf("NewClient(%q) succeeded", raw)
		}
	}
	c, err := sendcli.NewClient("HTTPS://Example.COM:8443/hub/", "token")
	if err != nil {
		t.Fatalf("NewClient with a valid URL: %v", err)
	}
	if got := c.MaxUploadBytes(); got != 0 {
		t.Fatalf("MaxUploadBytes = %d, want 0 before Connect", got)
	}
	_ = fmt.Sprintf("%v", c) // keep the client referenced
}
