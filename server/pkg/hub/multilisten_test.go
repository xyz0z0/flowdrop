package hub_test

// Multi-listener coverage: one Hub, one SQLite/attachment/auth/broker stack,
// several explicitly bound addresses. The two 127/8 loopback addresses stand in
// for a LAN IP plus a Tailscale IP so the suite is hermetic; this is not a real
// tailscale0 interface test (that remains on-hardware verification).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"flowdrop/pkg/hub"
)

type multiClient struct {
	base   string
	cookie string
	csrf   string
}

func pairMulti(t *testing.T, h *hub.Hub, base string) string {
	t.Helper()
	code, err := h.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	res, body := doJSON(t, http.MethodPost, base+"/api/v1/auth/pair",
		fmt.Sprintf(`{"code":%q,"device_name":"multi-test","native":true}`, code.Code),
		func(r *http.Request) { r.Header.Set("Origin", base) })
	if res.StatusCode != http.StatusOK {
		t.Fatalf("pair %s = %d body=%s", base, res.StatusCode, body)
	}
	var out struct {
		DeviceToken string `json:"device_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.DeviceToken == "" {
		t.Fatalf("pair %s returned no device token: %v body=%s", base, err, body)
	}
	return out.DeviceToken
}

// restoreMulti turns a saved device token into the cookies/CSRF of one origin.
// Cookies are host-scoped, so each listener gets its own restored session rather
// than sharing one cookie across addresses.
func restoreMulti(t *testing.T, base, token string) *multiClient {
	t.Helper()
	res, body := doJSON(t, http.MethodGet, base+"/api/v1/auth/session", "",
		func(r *http.Request) {
			r.Header.Set("Origin", base)
			r.Header.Set("Authorization", "Bearer "+token)
		})
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"authenticated":true`) {
		t.Fatalf("restore session %s = %d body=%s", base, res.StatusCode, body)
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode session: %v body=%s", err, body)
	}
	var pairs []string
	for _, c := range res.Cookies() {
		pairs = append(pairs, c.Name+"="+c.Value)
	}
	if out.CSRFToken == "" || len(pairs) == 0 {
		t.Fatalf("restore session %s is incomplete: %s", base, body)
	}
	return &multiClient{base: base, cookie: strings.Join(pairs, "; "), csrf: out.CSRFToken}
}

func (c *multiClient) request(t *testing.T, method, path, contentType string, body io.Reader, mutate ...func(*http.Request)) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Cookie", c.cookie)
	req.Header.Set("Origin", c.base)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-FlowDrop-CSRF", c.csrf)
	}
	for _, m := range mutate {
		m(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, b
}

func (c *multiClient) postJSON(t *testing.T, path, body string, mutate ...func(*http.Request)) (int, []byte) {
	t.Helper()
	return c.request(t, http.MethodPost, path, "application/json", strings.NewReader(body), mutate...)
}

func (c *multiClient) upload(t *testing.T, name string, content []byte) (int, string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	status, body := c.request(t, http.MethodPost, "/api/v1/items/upload", mw.FormDataContentType(), &buf)
	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &item); err != nil {
		return status, "", body
	}
	return status, item.ID, body
}

func listItemIDs(t *testing.T, c *multiClient) []string {
	t.Helper()
	status, body := c.request(t, http.MethodGet, "/api/v1/items?limit=100", "", nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d body=%s", status, body)
	}
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode list: %v body=%s", err, body)
	}
	ids := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

func dialEvents(t *testing.T, c *multiClient) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(c.base, "http") + "/api/v1/events"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{c.cookie},
			"Origin": []string{c.base},
		},
	})
	if err != nil {
		t.Fatalf("websocket dial %s: %v", wsURL, err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	var hello map[string]string
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if hello["type"] != "hello" {
		t.Fatalf("first frame = %v, want hello", hello)
	}
	return conn
}

func readEvent(t *testing.T, conn *websocket.Conn) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var ev struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := wsjson.Read(ctx, conn, &ev); err != nil {
		t.Fatalf("read event: %v", err)
	}
	return ev.Type, ev.ID
}

func TestDualListenSharedStateEventsAndSecurity(t *testing.T) {
	opts := testOptions(t, func(o *hub.Options) {
		o.Listen = "127.0.0.1:0"
		// 127.0.0.2 is another address on the loopback interface; it stands in
		// for the Tailscale/LAN address a real deployment would bind.
		o.AdditionalListen = []string{"127.0.0.2:0"}
	})
	h := mustNew(t, opts)
	if got := h.URLs(); got != nil {
		t.Fatalf("URLs() before Start = %v, want nil", got)
	}
	startHub(t, h)

	urls := h.URLs()
	if len(urls) != 2 {
		t.Fatalf("URLs() = %v, want 2 listeners", urls)
	}
	if h.URL() != urls[0] {
		t.Fatalf("URL() = %q, want primary URLs()[0] = %q", h.URL(), urls[0])
	}
	if !strings.HasPrefix(urls[0], "http://127.0.0.1:") || !strings.HasPrefix(urls[1], "http://127.0.0.2:") {
		t.Fatalf("URLs() = %v, want primary 127.0.0.1 first and 127.0.0.2 second", urls)
	}
	_, p0, _ := net.SplitHostPort(strings.TrimPrefix(urls[0], "http://"))
	_, p1, _ := net.SplitHostPort(strings.TrimPrefix(urls[1], "http://"))
	if p0 == p1 {
		t.Fatalf("port-0 listeners reused port %s; each :0 must get its own ephemeral port", p0)
	}
	// URLs must be a defensive copy.
	urls[0] = "http://mutated.invalid"
	if h.URLs()[0] == "http://mutated.invalid" || h.URL() == "http://mutated.invalid" {
		t.Fatal("URLs() exposed internal state; slice was not copied")
	}
	urls = h.URLs()

	for _, base := range urls {
		status, body := health(t, base)
		if status != http.StatusOK || !strings.Contains(body, "flowdrop-hub") {
			t.Fatalf("health %s = %d %q", base, status, body)
		}
	}

	token := pairMulti(t, h, urls[0])
	a := restoreMulti(t, urls[0], token)
	b := restoreMulti(t, urls[1], token)

	// One store: a text created on listener A is visible on B.
	status, body := a.postJSON(t, "/api/v1/items/text", `{"type":"text","text":"shared across listeners","sender_device":"client-a"}`)
	if status != http.StatusCreated {
		t.Fatalf("create on A = %d body=%s", status, body)
	}
	var textItem struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &textItem); err != nil {
		t.Fatalf("decode text item: %v", err)
	}
	if ids := listItemIDs(t, b); len(ids) != 1 || ids[0] != textItem.ID {
		t.Fatalf("list on B = %v, want [%s]", ids, textItem.ID)
	}

	// Central auth: A's session token is accepted at B when the client sends
	// B's Host/Origin (browsers do not share the cookie across hosts, native
	// clients may reuse their token).
	cross := &multiClient{base: b.base, cookie: a.cookie, csrf: a.csrf}
	status, body = cross.postJSON(t, "/api/v1/items/text", `{"type":"text","text":"A session on B"}`)
	if status != http.StatusCreated {
		t.Fatalf("A session on B = %d body=%s, want 201 (sessions are central)", status, body)
	}

	// One attachment store: upload on A, download the exact bytes on B.
	status, fileID, body := a.upload(t, "shared.bin", []byte("same attachment bytes"))
	if status != http.StatusCreated || fileID == "" {
		t.Fatalf("upload on A = %d body=%s", status, body)
	}
	status, downloaded := b.request(t, http.MethodGet, "/api/v1/items/"+fileID+"/download", "", nil)
	if status != http.StatusOK || !bytes.Equal(downloaded, []byte("same attachment bytes")) {
		t.Fatalf("download on B = %d %q", status, downloaded)
	}

	// One broker: both listeners' WebSockets receive the same create/delete.
	wsA := dialEvents(t, a)
	wsB := dialEvents(t, b)
	status, body = a.postJSON(t, "/api/v1/items/text", `{"type":"text","text":"event over both"}`)
	if status != http.StatusCreated {
		t.Fatalf("create event item = %d body=%s", status, body)
	}
	var eventItem struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &eventItem); err != nil {
		t.Fatalf("decode event item: %v", err)
	}
	for name, conn := range map[string]*websocket.Conn{"A": wsA, "B": wsB} {
		typ, id := readEvent(t, conn)
		if typ != "item.created" || id != eventItem.ID {
			t.Fatalf("WS %s event = %s/%s, want item.created/%s", name, typ, id, eventItem.ID)
		}
	}
	status, body = b.request(t, http.MethodDelete, "/api/v1/items/"+fileID, "", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete on B = %d body=%s", status, body)
	}
	for name, conn := range map[string]*websocket.Conn{"A": wsA, "B": wsB} {
		typ, id := readEvent(t, conn)
		if typ != "item.deleted" || id != fileID {
			t.Fatalf("WS %s event = %s/%s, want item.deleted/%s", name, typ, id, fileID)
		}
	}
	// Deletion is immediately consistent on the other listener.
	status, _ = a.request(t, http.MethodGet, "/api/v1/items/"+fileID+"/download", "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("download after delete on A = %d, want 404", status)
	}
	for _, id := range listItemIDs(t, a) {
		if id == fileID {
			t.Fatalf("deleted file item %s still listed on A", fileID)
		}
	}

	// Host/Origin/CSRF checks stay identical on every listener.
	before := listItemIDs(t, a)
	for _, base := range urls {
		res, _ := doJSON(t, http.MethodGet, base+"/api/v1/health", "", func(r *http.Request) {
			r.Host = "evil.example:8080"
		})
		if res.StatusCode != http.StatusMisdirectedRequest {
			t.Errorf("unknown Host on %s = %d, want 421", base, res.StatusCode)
		}
	}
	for _, base := range urls {
		client := a
		otherBase := urls[1]
		if base == urls[1] {
			client = b
			otherBase = urls[0]
		}
		cases := []struct {
			name   string
			mutate func(*http.Request)
		}{
			{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }},
			{"origin of another allowed listener", func(r *http.Request) { r.Header.Set("Origin", otherBase) }},
			{"missing csrf", func(r *http.Request) { r.Header.Del("X-FlowDrop-CSRF") }},
		}
		for _, tc := range cases {
			status, body := client.postJSON(t, "/api/v1/items/text", `{"type":"text","text":"must not persist"}`, tc.mutate)
			if status != http.StatusForbidden {
				t.Errorf("%s on %s = %d body=%s, want 403", tc.name, base, status, body)
			}
		}
	}
	if after := listItemIDs(t, a); len(after) != len(before) {
		t.Fatalf("rejected writes changed item count: before=%v after=%v", before, after)
	}

	// Stop releases every listener, the URLs, the database and the data-dir lock.
	if err := stopHub(t, h); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := h.URLs(); got != nil {
		t.Fatalf("URLs() after Stop = %v, want nil", got)
	}
	if got := h.URL(); got != "" {
		t.Fatalf("URL() after Stop = %q, want empty", got)
	}
	for name, conn := range map[string]*websocket.Conn{"A": wsA, "B": wsB} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var msg map[string]string
		err := wsjson.Read(ctx, conn, &msg)
		cancel()
		if err == nil {
			t.Fatalf("WebSocket on %s still readable after Stop", name)
		}
	}
	for _, base := range urls {
		if conn, err := net.DialTimeout("tcp", strings.TrimPrefix(base, "http://"), time.Second); err == nil {
			conn.Close()
			t.Errorf("listener %s still accepts connections after Stop", base)
		}
	}
	h2 := mustNew(t, opts)
	startHub(t, h2)
	stopQuietly(t, h2)
}

func TestDualListenSamePortDifferentIP(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	opts := testOptions(t, func(o *hub.Options) {
		o.Listen = fmt.Sprintf("127.0.0.1:%d", port)
		o.AdditionalListen = []string{fmt.Sprintf("127.0.0.2:%d", port)}
	})
	h := mustNew(t, opts)
	startHub(t, h)

	urls := h.URLs()
	if len(urls) != 2 {
		t.Fatalf("URLs() = %v, want 2 listeners", urls)
	}
	want0 := fmt.Sprintf("http://127.0.0.1:%d", port)
	want1 := fmt.Sprintf("http://127.0.0.2:%d", port)
	if urls[0] != want0 || urls[1] != want1 {
		t.Fatalf("URLs() = %v, want [%s %s] (same port, different IPs)", urls, want0, want1)
	}
	stopQuietly(t, h)
}

func TestAdditionalListenBindFailureRollsBackPrimary(t *testing.T) {
	primary, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve primary: %v", err)
	}
	primaryAddr := primary.Addr().String()
	primary.Close()

	blocker, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Fatalf("occupy second listener: %v", err)
	}
	defer blocker.Close()

	opts := testOptions(t, func(o *hub.Options) {
		o.Listen = primaryAddr
		o.AdditionalListen = []string{blocker.Addr().String()}
	})
	h := mustNew(t, opts)
	err = h.Start(context.Background())
	if err == nil {
		t.Fatal("Start with an occupied additional listener returned nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "in use") {
		t.Fatalf("Start err = %v, want a clear address-in-use message", err)
	}
	if !strings.Contains(err.Error(), blocker.Addr().String()) {
		t.Fatalf("Start err = %v, want the failing address %s", err, blocker.Addr().String())
	}
	if got := h.URLs(); got != nil {
		t.Fatalf("URLs() after failed Start = %v, want nil", got)
	}
	if got := h.URL(); got != "" {
		t.Fatalf("URL() after failed Start = %q, want empty", got)
	}

	// The primary listener bound earlier in this Start must be released again.
	ln, err := net.Listen("tcp", primaryAddr)
	if err != nil {
		t.Fatalf("primary port was not rolled back: %v", err)
	}
	ln.Close()

	// The externally owned listener is untouched.
	conn, err := net.DialTimeout("tcp", blocker.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("external listener was disturbed: %v", err)
	}
	conn.Close()

	// Stop still releases the database/data-dir lock after a failed Start.
	stopQuietly(t, h)
	blocker.Close()
	opts.Listen = "127.0.0.1:0"
	opts.AdditionalListen = []string{"127.0.0.2:0"}
	h2 := mustNew(t, opts)
	startHub(t, h2)
	stopQuietly(t, h2)
}

func TestAdditionalListenOptionsOverride(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"listen": "127.0.0.1:0",
		"additional_listen": ["127.0.0.2:0"]
	}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	no := false
	base := hub.Options{
		ConfigPath:       cfgPath,
		DataDir:          filepath.Join(root, "data"),
		CookieSecure:     &no,
		DiscoveryEnabled: &no,
	}

	// nil keeps the configured additional listener.
	h := mustNew(t, base)
	startHub(t, h)
	if urls := h.URLs(); len(urls) != 2 || !strings.HasPrefix(urls[1], "http://127.0.0.2:") {
		t.Fatalf("nil AdditionalListen URLs = %v, want configured 127.0.0.2 second", urls)
	}
	stopQuietly(t, h)

	// A non-nil empty slice explicitly overrides the config to one listener.
	empty := base
	empty.AdditionalListen = []string{}
	h = mustNew(t, empty)
	startHub(t, h)
	if urls := h.URLs(); len(urls) != 1 {
		t.Fatalf("empty AdditionalListen URLs = %v, want only the primary", urls)
	}
	stopQuietly(t, h)

	// A non-nil replacement overrides the config with exactly its entries.
	replaced := base
	replaced.AdditionalListen = []string{"127.0.0.3:0"}
	h = mustNew(t, replaced)
	startHub(t, h)
	urls := h.URLs()
	if len(urls) != 2 || !strings.HasPrefix(urls[1], "http://127.0.0.3:") {
		t.Fatalf("replaced AdditionalListen URLs = %v, want 127.0.0.3 second", urls)
	}
	stopQuietly(t, h)
}
