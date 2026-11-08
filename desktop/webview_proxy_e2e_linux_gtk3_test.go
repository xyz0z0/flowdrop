//go:build linux && cgo && gtk3 && flowdrop_webview_proxy_probe

package main

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flowdrop/desktop/internal/network"
)

// This is an opt-in end-to-end probe. It uses the real WebKitGTK build and the
// real desktop helper (configureDesktopWebViewProxy), not curl/Go networking.
//
// It is behind both the gtk3 tag and flowdrop_webview_proxy_probe so a normal
// build/test never compiles the probe. Run it with a display (Xvfb is fine).
// The three interesting invocations are:
//
//	# A. real host proxy kept enabled: LAN + optional Tailscale HTTP/WS succeed
//	xvfb-run -a env NO_AT_BRIDGE=1 FLOWDROP_WEBVIEW_PROXY_E2E=1 \
//	  FLOWDROP_WEBVIEW_PROXY_E2E_BINDS=192.168.31.184,100.125.168.63 \
//	  go test -tags "gtk3 flowdrop_webview_proxy_probe" \
//	    -run TestWebViewProxyE2E -v .
//
//	# B. proxy points at a dead port, empty XDG config: helper still connects
//	EMPTY=$(mktemp -d)
//	xvfb-run -a env NO_AT_BRIDGE=1 FLOWDROP_WEBVIEW_PROXY_E2E=1 XDG_CONFIG_HOME="$EMPTY" \
//	  http_proxy=http://127.0.0.1:1 https_proxy=http://127.0.0.1:1 \
//	  no_proxy= NO_PROXY= go test -tags "gtk3 flowdrop_webview_proxy_probe" \
//	    -run TestWebViewProxyE2E -v .
//
//	# C. same dead proxy but skip the helper: WebKit must fail (control)
//	xvfb-run -a env NO_AT_BRIDGE=1 FLOWDROP_WEBVIEW_PROXY_E2E=1 \
//	  FLOWDROP_WEBVIEW_PROXY_E2E_EXPECT=fail XDG_CONFIG_HOME="$EMPTY" \
//	  http_proxy=http://127.0.0.1:1 https_proxy=http://127.0.0.1:1 \
//	  no_proxy= NO_PROXY= go test -tags "gtk3 flowdrop_webview_proxy_probe" \
//	    -run TestWebViewProxyE2E -v .
//
// The test never writes kioslaverc/GSettings/environment files. Invocation B/C
// only override the test process environment.
func TestWebViewProxyE2E(t *testing.T) {
	if os.Getenv("FLOWDROP_WEBVIEW_PROXY_E2E") != "1" {
		t.Skip("opt-in WebKitGTK proxy probe; set FLOWDROP_WEBVIEW_PROXY_E2E=1")
	}
	// GTK/WebKit calls must stay on one OS thread; a migrated goroutine
	// segfaults inside gtk_main_iteration_do.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	expectOK := os.Getenv("FLOWDROP_WEBVIEW_PROXY_E2E_EXPECT") != "fail"

	binds := probeBinds(t)
	if len(binds) == 0 {
		t.Fatal("no bind address available")
	}

	if expectOK {
		if err := configureDesktopWebViewProxy(); err != nil {
			t.Fatalf("configureDesktopWebViewProxy: %v", err)
		}
	}

	if os.Getenv("NO_AT_BRIDGE") == "" && os.Getenv("AT_SPI_BUS_ADDRESS") == "" {
		// The ATK bridge is loaded before test code runs and dereferences a
		// missing accessibility bus inside gtk_main_iteration_do. The env var
		// must be set before process start.
		t.Fatal("headless session without an accessibility bus: rerun with NO_AT_BRIDGE=1")
	}
	if !probeGTKInit() {
		t.Fatal("gtk_init_check failed (needs DISPLAY/Wayland; run under xvfb-run)")
	}
	view := probeNewView()
	window := probeNewWindow(view)
	defer probeDestroyWindow(window)

	for _, ip := range binds {
		srv := newProbeServer(t, ip)
		defer srv.close()

		t.Logf("loading %s (real proxy env: http_proxy=%q, no_proxy=%q)",
			srv.url, os.Getenv("http_proxy"), os.Getenv("no_proxy"))
		title := loadAndWait(view, srv.url, srv.token, 12*time.Second)
		t.Logf("title=%q websocket101=%v remotes=%v", title, srv.upgraded.Load(), srv.remotes())

		if expectOK {
			if title != "OK-"+srv.token {
				t.Fatalf("WebView did not complete HTTP+WebSocket to %s: title=%q", srv.url, title)
			}
			if !srv.upgraded.Load() {
				t.Fatalf("WebSocket upgrade was not observed for %s", srv.url)
			}
			for _, remote := range srv.remotes() {
				if remote != ip {
					t.Fatalf("HTTP/WS arrived from %q, want direct connection from %q (a proxy hop uses a different source)", remote, ip)
				}
			}
		} else {
			if title == "OK-"+srv.token {
				t.Fatalf("control run unexpectedly reached %s; the dead proxy was not used", srv.url)
			}
			if srv.upgraded.Load() {
				t.Fatalf("control run unexpectedly completed a WebSocket upgrade for %s", srv.url)
			}
		}
	}
}

func probeBinds(t *testing.T) []string {
	t.Helper()
	if raw := strings.TrimSpace(os.Getenv("FLOWDROP_WEBVIEW_PROXY_E2E_BINDS")); raw != "" {
		var out []string
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	list, err := network.List()
	if err != nil {
		t.Fatalf("list interfaces: %v", err)
	}
	var out []string
	for _, iface := range list {
		if iface.Address == "127.0.0.1" {
			continue
		}
		out = append(out, iface.Address)
		if len(out) == 2 {
			break
		}
	}
	return out
}

type probeServer struct {
	url      string
	token    string
	upgraded atomic.Bool

	mu     sync.Mutex
	remote []string

	srv *http.Server
	ln  net.Listener
}

func newProbeServer(t *testing.T, ip string) *probeServer {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Fatalf("listen on %s: %v", ip, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ps := &probeServer{
		url:   fmt.Sprintf("http://%s/", ln.Addr().String()),
		token: fmt.Sprintf("%d", port),
		ln:    ln,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ps.recordRemote(r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, probePage, ps.token, ps.token)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ps.recordRemote(r)
		ps.upgrade(w, r)
	})
	ps.srv = &http.Server{Handler: mux}
	go ps.srv.Serve(ln)
	return ps
}

func (ps *probeServer) close() {
	if ps.srv != nil {
		ps.srv.Close()
	}
}

func (ps *probeServer) recordRemote(r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ps.mu.Lock()
	ps.remote = append(ps.remote, host)
	ps.mu.Unlock()
}

func (ps *probeServer) remotes() []string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return append([]string(nil), ps.remote...)
}

// upgrade performs the smallest RFC 6455 handshake that satisfies WebKit: a
// 101 response plus one text frame. A successful 101 is what the WebView's
// onopen depends on, so this is a real WebSocket upgrade, not a mock.
func (ps *probeServer) upgrade(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || key == "" {
		http.Error(w, "not a websocket handshake", http.StatusBadRequest)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unavailable", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	if err := buf.Flush(); err != nil {
		conn.Close()
		return
	}
	ps.upgraded.Store(true)
	// Text frame "hi"; the 101 already triggers onopen, the frame keeps it alive.
	conn.Write([]byte{0x81, 0x02, 'h', 'i'})
	time.AfterFunc(3*time.Second, func() { conn.Close() })
}

func loadAndWait(view uintptr, url, token string, timeout time.Duration) string {
	probeLoad(view, url)

	want := "OK-" + token
	deadline := time.Now().Add(timeout)
	title := ""
	for time.Now().Before(deadline) {
		probeIterate()
		title = probeTitle(view)
		if title == want {
			// One extra turn lets WebKit finish the page/WS teardown cleanly.
			probeIterate()
			return title
		}
		if strings.HasPrefix(title, "ERR-") || strings.HasPrefix(title, "TIMEOUT-") {
			return title
		}
		time.Sleep(20 * time.Millisecond)
	}
	return title
}

const probePage = `<!doctype html>
<meta charset="utf-8">
<title>PENDING-%s</title>
<script>
(function () {
  var token = %q;
  var done = false;
  function set(t) { if (!done) { done = true; document.title = t + "-" + token; } }
  try {
    var scheme = location.protocol === "https:" ? "wss://" : "ws://";
    var ws = new WebSocket(scheme + location.host + "/ws");
    ws.onopen = function () { set("OK"); };
    ws.onerror = function () { set("ERR"); };
    ws.onclose = function () { set("ERR"); };
  } catch (e) {
    set("ERR");
  }
  setTimeout(function () { set("TIMEOUT"); }, 6000);
})();
</script>
`
