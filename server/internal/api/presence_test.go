package api

// Online device list: GET /api/v1/presence and the presence.changed invalidation
// notice. These tests use real HTTP + real WebSocket connections on a real
// listener, because "online" means exactly "holds a live authenticated event
// stream", which cannot be simulated by unit-testing the registry alone.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"flowdrop/internal/config"
)

// presenceTestDevice is one paired device with a native token so it can open an
// event stream over both HTTP (presence snapshot) and WebSocket.
type presenceTestDevice struct {
	ID    string
	Name  string
	Token string
}

func newPresenceHarness(t *testing.T) *harness {
	t.Helper()
	// Presence tests pair several devices from one source IP; the default
	// per-IP pairing limit (5/min) is not what they exercise.
	return newHarness(t, func(c *config.Config) { c.PairLimitPerMinute = 1000 })
}

// pairPresenceDevice registers a native device and returns its Bearer token.
func pairPresenceDevice(t *testing.T, h *harness, name string) presenceTestDevice {
	t.Helper()
	body := fmt.Sprintf(`{"code":%q,"device_name":%q,"native":true}`, h.newCode(), name)
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/api/v1/auth/pair", strings.NewReader(body))
	if err != nil {
		t.Fatalf("pair request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.srv.URL)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("pair %s: %v", name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("pair %s = %d", name, res.StatusCode)
	}
	var out sessionResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode pair: %v", err)
	}
	if out.DeviceToken == "" || out.DeviceID == "" {
		t.Fatalf("native pairing returned no token/id: %+v", out)
	}
	return presenceTestDevice{ID: out.DeviceID, Name: name, Token: out.DeviceToken}
}

// dialPresence opens an authenticated event stream and asserts hello is still
// the first frame.
func dialPresence(t *testing.T, h *harness, dev presenceTestDevice) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/api/v1/events"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin":        []string{h.srv.URL},
			"Authorization": []string{"Bearer " + dev.Token},
		},
	})
	if err != nil {
		t.Fatalf("dial ws %s: %v", dev.Name, err)
	}
	var first map[string]any
	if err := wsReadJSON(ctx, conn, &first); err != nil {
		t.Fatalf("read hello %s: %v", dev.Name, err)
	}
	if first["type"] != "hello" {
		t.Fatalf("first frame for %s = %v, want hello", dev.Name, first)
	}
	return conn
}

// wsReadJSON reads one frame with a hard deadline so a missing event fails the
// test instead of hanging.
func wsReadJSON(ctx context.Context, conn *websocket.Conn, dst any) error {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

// presenceBody fetches GET /api/v1/presence as dev and returns the decoded
// entries plus the raw top-level JSON object for key-set assertions.
func presenceBody(t *testing.T, h *harness, dev presenceTestDevice) ([]presenceDevice, map[string]json.RawMessage) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/presence", nil)
	if err != nil {
		t.Fatalf("presence request: %v", err)
	}
	req.Header.Set("Origin", h.srv.URL)
	if dev.Token != "" {
		req.Header.Set("Authorization", "Bearer "+dev.Token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presence: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("presence status = %d, want 200", res.StatusCode)
	}
	var top map[string]json.RawMessage
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read presence body: %v", err)
	}
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("decode presence: %v body=%s", err, body)
	}
	raw, ok := top["online"]
	if !ok {
		t.Fatalf("presence body has no online key: %s", body)
	}
	if string(raw) == "null" {
		t.Fatalf("online must be [] not null: %s", body)
	}
	var online []presenceDevice
	if err := json.Unmarshal(raw, &online); err != nil {
		t.Fatalf("decode online: %v body=%s", err, raw)
	}
	return online, top
}

// waitPresence polls until the snapshot satisfies want, so tests never race the
// server's connection bookkeeping. It fails after 5s.
func waitPresence(t *testing.T, h *harness, dev presenceTestDevice, want string, ok func([]presenceDevice) bool) []presenceDevice {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last []presenceDevice
	for time.Now().Before(deadline) {
		online, _ := presenceBody(t, h, dev)
		last = online
		if ok(online) {
			return online
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("presence never matched %q; last=%+v", want, last)
	return nil
}

func presenceIDs(online []presenceDevice) []string {
	ids := make([]string, 0, len(online))
	for _, d := range online {
		ids = append(ids, d.ID)
	}
	return ids
}

// ---------- authentication & shape ----------

func TestPresenceRequiresAuth(t *testing.T) {
	h := newPresenceHarness(t)
	res := h.do(http.MethodGet, "/api/v1/presence", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated presence = %d, want 401", res.StatusCode)
	}
}

func TestPresenceEmptyIsArrayNotNull(t *testing.T) {
	h := newPresenceHarness(t)
	dev := pairPresenceDevice(t, h, "laptop")
	online, top := presenceBody(t, h, dev)
	if len(online) != 0 {
		t.Fatalf("device without an event stream is online: %+v", online)
	}
	if string(top["online"]) != "[]" {
		t.Fatalf("empty online list = %s, want []", top["online"])
	}
	if len(top) != 1 {
		t.Fatalf("presence top-level keys = %v, want only online", top)
	}
}

// TestPresenceKeySetHasNoSensitiveFields asserts the frozen shape: exactly
// id/name/current per entry, no token, IP, connection count or timestamps.
func TestPresenceKeySetHasNoSensitiveFields(t *testing.T) {
	h := newPresenceHarness(t)
	dev := pairPresenceDevice(t, h, "laptop")
	conn := dialPresence(t, h, dev)
	defer conn.Close(websocket.StatusNormalClosure, "")

	waitPresence(t, h, dev, "self online", func(online []presenceDevice) bool {
		return len(online) == 1
	})

	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/presence", nil)
	req.Header.Set("Origin", h.srv.URL)
	req.Header.Set("Authorization", "Bearer "+dev.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presence: %v", err)
	}
	defer res.Body.Close()
	var raw struct {
		Online []map[string]any `json:"online"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw.Online) != 1 {
		t.Fatalf("online = %+v", raw.Online)
	}
	entry := raw.Online[0]
	if len(entry) != 3 {
		t.Fatalf("entry keys = %v, want exactly id/name/current", entry)
	}
	for _, key := range []string{"id", "name", "current"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("entry missing %q: %v", key, entry)
		}
	}
	for _, leak := range []string{"token", "ip", "conns", "connections", "last_seen_at", "created_at"} {
		if _, ok := entry[leak]; ok {
			t.Fatalf("entry leaked %q: %v", leak, entry)
		}
	}
}

// ---------- per-device connection tracking ----------

func TestPresenceTracksFirstAndLastConnection(t *testing.T) {
	h := newPresenceHarness(t)
	dev := pairPresenceDevice(t, h, "laptop")

	first := dialPresence(t, h, dev)
	waitPresence(t, h, dev, "online after first stream", func(online []presenceDevice) bool {
		return len(online) == 1 && online[0].ID == dev.ID && online[0].Current
	})

	// A second tab of the same device must not add a second row.
	second := dialPresence(t, h, dev)
	online := waitPresence(t, h, dev, "one row for two streams", func(online []presenceDevice) bool {
		return len(online) == 1
	})
	if online[0].Name != dev.Name {
		t.Fatalf("row = %+v, want name %q", online[0], dev.Name)
	}

	// Closing one of two streams keeps the device online.
	first.Close(websocket.StatusNormalClosure, "")
	time.Sleep(300 * time.Millisecond)
	online, _ = presenceBody(t, h, dev)
	if len(online) != 1 || online[0].ID != dev.ID {
		t.Fatalf("after closing one of two streams: %+v, want device online", online)
	}

	// The last stream going away is what makes it offline.
	second.Close(websocket.StatusNormalClosure, "")
	waitPresence(t, h, dev, "offline after last stream", func(online []presenceDevice) bool {
		return len(online) == 0
	})
}

func TestPresenceSameNameDifferentDevices(t *testing.T) {
	h := newPresenceHarness(t)
	beta := pairPresenceDevice(t, h, "same-name")
	alpha := pairPresenceDevice(t, h, "alpha")
	sameA := pairPresenceDevice(t, h, "same-name")

	connB := dialPresence(t, h, beta)
	defer connB.Close(websocket.StatusNormalClosure, "")
	connA := dialPresence(t, h, alpha)
	defer connA.Close(websocket.StatusNormalClosure, "")
	connS := dialPresence(t, h, sameA)
	defer connS.Close(websocket.StatusNormalClosure, "")

	online := waitPresence(t, h, beta, "all three online", func(online []presenceDevice) bool {
		return len(online) == 3
	})
	// Same name, different device IDs: two rows, never merged. Ordered by name
	// then id, and current flags only the requester.
	if online[0].Name != "alpha" || online[0].ID != alpha.ID || online[0].Current {
		t.Fatalf("first row = %+v, want alpha (not current)", online[0])
	}
	same := []presenceDevice{online[1], online[2]}
	if same[0].Name != "same-name" || same[1].Name != "same-name" {
		t.Fatalf("rows = %+v, want two same-name rows", online)
	}
	if same[0].ID == same[1].ID {
		t.Fatalf("same-name devices merged: %+v", same)
	}
	if same[0].ID > same[1].ID {
		t.Fatalf("same-name rows not ordered by id: %+v", same)
	}
	// current follows the requesting device, per request.
	byID := map[string]presenceDevice{same[0].ID: same[0], same[1].ID: same[1]}
	requesterRows := byID
	if !requesterRows[beta.ID].Current || requesterRows[sameA.ID].Current {
		t.Fatalf("current not isolated to requester %s: %+v", beta.ID, online)
	}
	other, _ := presenceBody(t, h, sameA)
	otherByID := map[string]presenceDevice{}
	for _, d := range other {
		otherByID[d.ID] = d
	}
	if !otherByID[sameA.ID].Current || otherByID[beta.ID].Current {
		t.Fatalf("current not isolated to requester %s: %+v", sameA.ID, other)
	}
}

// ---------- revocation ----------

func TestPresenceRevocationDropsImmediatelyAndClosesStream(t *testing.T) {
	h := newPresenceHarness(t)
	doomed := pairPresenceDevice(t, h, "doomed")
	watcher := pairPresenceDevice(t, h, "watcher")

	doomedConn := dialPresence(t, h, doomed)
	defer doomedConn.Close(websocket.StatusNormalClosure, "")
	watchConn := dialPresence(t, h, watcher)
	defer watchConn.Close(websocket.StatusNormalClosure, "")

	waitPresence(t, h, watcher, "both online", func(online []presenceDevice) bool {
		return len(online) == 2
	})

	if err := h.api.RevokeDevice(doomed.ID); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	// Immediately, without waiting for the socket to close or the 500ms
	// coalescing window: the revoked device is gone from the list.
	online, _ := presenceBody(t, h, watcher)
	if ids := presenceIDs(online); len(ids) != 1 || ids[0] != watcher.ID {
		t.Fatalf("after revoke online = %+v, want only watcher", online)
	}

	// The open stream is closed with the existing policy-violation status (1008).
	closeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	for {
		_, _, err := doomedConn.Read(closeCtx)
		if err == nil {
			t.Fatalf("revoked stream still receiving frames")
		}
		if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation {
			t.Fatalf("close status = %v (err %v), want 1008", status, err)
		}
		break
	}

	// Idempotent: the second revoke reports unauthorized and changes nothing.
	if err := h.api.RevokeDevice(doomed.ID); err == nil {
		t.Fatal("second revoke should report unauthorized")
	}
	online, _ = presenceBody(t, h, watcher)
	if ids := presenceIDs(online); len(ids) != 1 || ids[0] != watcher.ID {
		t.Fatalf("after second revoke online = %+v", online)
	}
}

// TestPresenceSnapshotFiltersStoreLevelRevocation covers the other-process
// revocation path (local CLI): the registry may still hold the stream for up to
// the 1s revalidation interval, but the snapshot must never list a revoked
// device because it is intersected with the pairing store.
func TestPresenceSnapshotFiltersStoreLevelRevocation(t *testing.T) {
	h := newPresenceHarness(t)
	revoked := pairPresenceDevice(t, h, "revoked-by-cli")
	watcher := pairPresenceDevice(t, h, "watcher")

	revokedConn := dialPresence(t, h, revoked)
	defer revokedConn.Close(websocket.StatusNormalClosure, "")
	watchConn := dialPresence(t, h, watcher)
	defer watchConn.Close(websocket.StatusNormalClosure, "")

	waitPresence(t, h, watcher, "both online", func(online []presenceDevice) bool {
		return len(online) == 2
	})

	// Store-level revocation, exactly like the CLI process does it.
	if err := h.pairing.RevokeDevice(revoked.ID); err != nil {
		t.Fatalf("store revoke: %v", err)
	}
	online, _ := presenceBody(t, h, watcher)
	if ids := presenceIDs(online); len(ids) != 1 || ids[0] != watcher.ID {
		t.Fatalf("snapshot after store revoke = %+v, want only watcher", online)
	}
}

// ---------- presence.changed ----------

// TestPresenceChangedIsCoalescedAndLeavesItemsAlone asserts the invalidation
// notice is a bare type-only event, that many joins inside one window produce a
// single notice, and that item events still arrive unchanged.
func TestPresenceChangedIsCoalescedAndLeavesItemsAlone(t *testing.T) {
	h := newPresenceHarness(t)
	watcher := pairPresenceDevice(t, h, "watcher")
	// The item is created by a browser-paired device so the harness' cookie
	// jar and CSRF token drive the write path.
	h.pair()

	watchConn := dialPresence(t, h, watcher)
	defer watchConn.Close(websocket.StatusNormalClosure, "")

	// Two more devices join inside the same 500ms window.
	connB := dialPresence(t, h, pairPresenceDevice(t, h, "b"))
	defer connB.Close(websocket.StatusNormalClosure, "")
	connC := dialPresence(t, h, pairPresenceDevice(t, h, "c"))
	defer connC.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var notice map[string]any
	if err := wsReadJSON(ctx, watchConn, &notice); err != nil {
		t.Fatalf("read presence.changed: %v", err)
	}
	if notice["type"] != "presence.changed" {
		t.Fatalf("event = %v, want presence.changed", notice)
	}
	// The notice carries no device list and nothing device-specific: the broker's
	// shared Event shape only contributes an empty id.
	if id, ok := notice["id"]; !ok || id != "" {
		t.Fatalf("presence.changed carries a payload id: %v", notice)
	}
	if len(notice) != 2 {
		t.Fatalf("presence.changed carries extra fields: %v", notice)
	}

	// The next frame must be the item event, not a second notice: this proves
	// both coalescing (one notice for two joins) and that item delivery is
	// unaffected.
	item := h.createText("text", "after joins", "author")
	var itemEvent map[string]any
	if err := wsReadJSON(ctx, watchConn, &itemEvent); err != nil {
		t.Fatalf("read item.created: %v", err)
	}
	if itemEvent["type"] != "item.created" || itemEvent["id"] != item.ID {
		t.Fatalf("frame after notice = %v, want item.created %s", itemEvent, item.ID)
	}

	// Leaving: closing both extra streams collapses into one more notice, and
	// the watcher's own stream keeps it online.
	connB.Close(websocket.StatusNormalClosure, "")
	connC.Close(websocket.StatusNormalClosure, "")
	var leaveNotice map[string]any
	if err := wsReadJSON(ctx, watchConn, &leaveNotice); err != nil {
		t.Fatalf("read leave notice: %v", err)
	}
	if leaveNotice["type"] != "presence.changed" {
		t.Fatalf("leave event = %v, want presence.changed", leaveNotice)
	}
	online, _ := presenceBody(t, h, watcher)
	if ids := presenceIDs(online); len(ids) != 1 || ids[0] != watcher.ID {
		t.Fatalf("after leaves online = %+v, want only watcher", online)
	}
}

// ---------- registry concurrency ----------

func TestPresenceRegistryConcurrentJoinLeaveAndDrop(t *testing.T) {
	var mu sync.Mutex
	notices := 0
	p := newPresence(time.Millisecond, func() {
		mu.Lock()
		notices++
		mu.Unlock()
	})

	const devices = 8
	const streamsPerDevice = 12

	var wg sync.WaitGroup
	for d := 0; d < devices; d++ {
		id := fmt.Sprintf("device-%d", d)
		for s := 0; s < streamsPerDevice; s++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				connID, _ := p.join(id)
				p.leave(id, connID)
				// Double leave must be a no-op, not a removal of a newer stream.
				p.leave(id, connID)
			}()
		}
	}
	// Revocation races with the joins/leaves above.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < devices; i++ {
			p.drop(fmt.Sprintf("device-%d", i))
		}
	}()
	wg.Wait()

	for d := 0; d < devices; d++ {
		if p.deviceOnline(fmt.Sprintf("device-%d", d)) {
			t.Errorf("device-%d still online after all streams left", d)
		}
	}
	p.mu.Lock()
	remaining := len(p.byDevice)
	p.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("registry still has %d devices", remaining)
	}

	// Effective changes must still produce (coalesced) invalidation notices.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		seen := notices
		mu.Unlock()
		if seen > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no presence notice was published for real join/leave changes")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestPresenceRegistryDoesNotDropNewerStream pins the revoke/join race: a
// stream registered after drop must stay registered until it leaves itself.
func TestPresenceRegistryDoesNotDropNewerStream(t *testing.T) {
	p := newPresence(presenceWindow, func() {})

	older, _ := p.join("dev")
	p.drop("dev")
	if p.deviceOnline("dev") {
		t.Fatal("stream stayed registered after drop")
	}

	newer, first := p.join("dev")
	if !first {
		t.Fatal("join after drop should be the device's first stream again")
	}
	if !p.deviceOnline("dev") {
		t.Fatal("new stream missing after it re-registered")
	}
	// The old stream leaving must not remove the new one.
	p.leave("dev", older)
	if !p.deviceOnline("dev") {
		t.Fatal("leave of an older stream removed the newer one")
	}
	p.leave("dev", newer)
	if p.deviceOnline("dev") {
		t.Fatal("device still online after its last stream left")
	}
}
