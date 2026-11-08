//go:build linux

package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestA11yUnixTarget(t *testing.T) {
	cases := []struct {
		address string
		target  string
		wantErr bool
	}{
		{"unix:path=/run/user/1000/at-spi/bus_0", "/run/user/1000/at-spi/bus_0", false},
		{"unix:path=/run/user/1000/at-spi/bus_0,guid=abc123", "/run/user/1000/at-spi/bus_0", false},
		{"unix:abstract=/tmp/at-spi-abstract", "@/tmp/at-spi-abstract", false},
		{"unix:path=", "", true},
		{"tcp:host=127.0.0.1,port=1234", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		_, target, err := a11yUnixTarget(tc.address)
		if tc.wantErr {
			if err == nil {
				t.Errorf("a11yUnixTarget(%q) = %q, want error", tc.address, target)
			}
			continue
		}
		if err != nil {
			t.Errorf("a11yUnixTarget(%q) error: %v", tc.address, err)
			continue
		}
		if target != tc.target {
			t.Errorf("a11yUnixTarget(%q) target = %q, want %q", tc.address, target, tc.target)
		}
	}
}

// A socket that accepts the connection but never answers must be reported as
// unhealthy quickly, not hang the probe.
func TestProbeA11yBusUnresponsiveSocket(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "bus_0")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		close(accepted)
		// Keep the connection open without ever answering the D-Bus handshake.
		defer conn.Close()
		buf := make([]byte, 64)
		_, _ = conn.Read(buf)
		time.Sleep(5 * time.Second)
	}()

	t.Setenv("AT_SPI_BUS_ADDRESS", "unix:path="+socketPath)
	start := time.Now()
	ok, reason := probeA11yBus(400 * time.Millisecond)
	if ok {
		t.Fatalf("unresponsive accessibility bus reported healthy")
	}
	if reason == "" {
		t.Fatalf("unresponsive accessibility bus reported without a reason")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("probe took %v, want bounded by the timeout", elapsed)
	}
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatalf("probe never connected to the socket")
	}
}

func TestProbeA11yBusMissingSocket(t *testing.T) {
	t.Setenv("AT_SPI_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "missing"))
	ok, reason := probeA11yBus(400 * time.Millisecond)
	if ok {
		t.Fatalf("missing accessibility bus reported healthy")
	}
	if !strings.Contains(reason, "无法连接") {
		t.Fatalf("reason = %q, want a connection error", reason)
	}
}

func TestGuardAccessibilityBridgePolicy(t *testing.T) {
	reset := func() {
		desktop.a11yFallback.Store("")
		_ = os.Unsetenv("NO_AT_BRIDGE")
	}
	reset()
	defer reset()

	// off: disable immediately, without probing.
	t.Setenv("FLOWDROP_DESKTOP_A11Y", "off")
	t.Setenv("NO_AT_BRIDGE", "")
	guardAccessibilityBridge()
	if got := os.Getenv("NO_AT_BRIDGE"); got != "1" {
		t.Fatalf("FLOWDROP_DESKTOP_A11Y=off: NO_AT_BRIDGE=%q, want 1", got)
	}
	if reason := desktop.a11yFallbackReason(); !strings.Contains(reason, "FLOWDROP_DESKTOP_A11Y=off") {
		t.Fatalf("FLOWDROP_DESKTOP_A11Y=off: fallback reason = %q", reason)
	}

	// on: keep accessibility, never disable.
	reset()
	t.Setenv("FLOWDROP_DESKTOP_A11Y", "on")
	guardAccessibilityBridge()
	if got := os.Getenv("NO_AT_BRIDGE"); got != "" {
		t.Fatalf("FLOWDROP_DESKTOP_A11Y=on: NO_AT_BRIDGE=%q, want unset", got)
	}
	if reason := desktop.a11yFallbackReason(); reason != "" {
		t.Fatalf("FLOWDROP_DESKTOP_A11Y=on: fallback reason = %q, want empty", reason)
	}

	// A caller-provided NO_AT_BRIDGE (e.g. the launcher) is respected.
	reset()
	t.Setenv("FLOWDROP_DESKTOP_A11Y", "")
	t.Setenv("NO_AT_BRIDGE", "1")
	guardAccessibilityBridge()
	if reason := desktop.a11yFallbackReason(); reason != "" {
		t.Fatalf("NO_AT_BRIDGE already set: fallback reason = %q, want empty", reason)
	}
}

func TestDesktopAccessibilityWarning(t *testing.T) {
	if got := desktopAccessibilityWarning(""); got != "" {
		t.Fatalf("empty reason should produce no warning, got %q", got)
	}
	got := desktopAccessibilityWarning("AT-SPI 无障碍总线无响应")
	if !strings.Contains(got, "AT-SPI 无障碍总线无响应") {
		t.Fatalf("warning does not include the reason: %q", got)
	}
	if !strings.Contains(got, "注销并重新登录") {
		t.Fatalf("warning does not tell the user how to recover: %q", got)
	}
	if !strings.Contains(got, "NO_AT_BRIDGE=1") || !strings.Contains(got, "FLOWDROP_DESKTOP_A11Y=on") {
		t.Fatalf("warning does not list the manual switches: %q", got)
	}
}
