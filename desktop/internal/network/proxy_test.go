package network

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY", "XDG_CONFIG_HOME"} {
		old, had := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		key := key
		t.Cleanup(func() {
			if had {
				os.Setenv(key, old)
			}
		})
	}
}

func TestProxyWarningLoopbackNeverWarns(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("http_proxy", "http://127.0.0.1:10808")
	for _, host := range []string{"127.0.0.1", "localhost", ""} {
		if got := ProxyWarning(host); got != "" {
			t.Fatalf("ProxyWarning(%q) = %q, want empty", host, got)
		}
	}
}

func TestProxyWarningDetectsUnproxiedLAN(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("http_proxy", "http://127.0.0.1:10808")
	got := ProxyWarning("192.168.31.184")
	if !strings.Contains(got, "192.168.31.184") || !strings.Contains(got, "no_proxy") {
		t.Fatalf("ProxyWarning = %q, want an actionable message naming the address", got)
	}
	// A detected proxy is a risk, not proof: the message must not claim that
	// every HTTP proxy breaks the WebSocket handshake.
	if strings.Contains(got, "会经过代理并被改写握手") {
		t.Fatalf("ProxyWarning still asserts certain WebSocket failure: %q", got)
	}
	if !strings.Contains(got, "风险提示") || !strings.Contains(got, "426") {
		t.Fatalf("ProxyWarning should distinguish risk from an actual failed handshake: %q", got)
	}
}

func TestProxyWarningRespectsNoProxy(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("http_proxy", "http://127.0.0.1:10808")
	t.Setenv("no_proxy", "localhost,192.168.31.184")
	if got := ProxyWarning("192.168.31.184"); got != "" {
		t.Fatalf("ProxyWarning with exempt host = %q, want empty", got)
	}
	t.Setenv("no_proxy", "192.168.31.0/24")
	if got := ProxyWarning("192.168.31.184"); got != "" {
		t.Fatalf("ProxyWarning with exempt CIDR = %q, want empty", got)
	}
	t.Setenv("no_proxy", "*")
	if got := ProxyWarning("10.0.0.5"); got != "" {
		t.Fatalf("ProxyWarning with no_proxy=* = %q, want empty", got)
	}
}

func TestProxyWarningReadsKDESettings(t *testing.T) {
	clearProxyEnv(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	kioslaverc := filepath.Join(dir, "kioslaverc")
	body := "[Proxy Settings]\nProxyType=1\nhttpProxy=http://127.0.0.1:10808\nNoProxyFor=localhost,127.0.0.0/8,::1\n"
	if err := os.WriteFile(kioslaverc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ProxyWarning("192.168.31.184")
	if !strings.Contains(got, "192.168.31.184") {
		t.Fatalf("ProxyWarning(KDE manual proxy) = %q, want a warning", got)
	}
	// Exempting the address silences it.
	body = strings.Replace(body, "NoProxyFor=localhost", "NoProxyFor=192.168.31.184,localhost", 1)
	if err := os.WriteFile(kioslaverc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ProxyWarning("192.168.31.184"); got != "" {
		t.Fatalf("ProxyWarning with KDE exemption = %q, want empty", got)
	}
	// ProxyType=0 (none) also silences it.
	body = "[Proxy Settings]\nProxyType=0\nhttpProxy=http://127.0.0.1:10808\n"
	if err := os.WriteFile(kioslaverc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ProxyWarning("192.168.31.184"); got != "" {
		t.Fatalf("ProxyWarning with proxy disabled = %q, want empty", got)
	}
}

func TestProxyWarningNoProxyConfigured(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if got := ProxyWarning("192.168.31.184"); got != "" {
		t.Fatalf("ProxyWarning without any proxy = %q, want empty", got)
	}
}
