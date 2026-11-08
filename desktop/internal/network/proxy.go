package network

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ProxyWarning explains an environment problem the desktop cannot fix for the
// user: if the machine routes HTTP through a proxy that does not exempt the
// Hub's own address, the WebView's traffic (notably the WebSocket upgrade) is
// proxied too. The Hub never edits the host's proxy configuration; it only
// reports the exact address to exempt.
//
// It returns "" when no proxy is configured or the Hub host is already exempt.
func ProxyWarning(hubHost string) string {
	p := detectProxy()
	if p == "" {
		return ""
	}
	if p == "none" {
		return ""
	}
	if hubHost == "" {
		return ""
	}
	host := hubHost
	if ip := net.ParseIP(hubHost); ip != nil {
		host = ip.String()
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return ""
	}
	if exemptByNoProxy(host) {
		return ""
	}
	if exemptByKDENoProxy(host) {
		return ""
	}
	return "当前系统配置了 HTTP 代理（" + shortenProxy(p) + "），但代理绕过列表未包含 " + host + "。\n" +
		"WebView 的 WebSocket 会经过代理并被改写握手，收件箱将一直显示「已离线，重连中」。\n" +
		"请把 " + host + " 加入系统代理的「忽略主机 / no_proxy」后重启 FlowDrop；本应用不会修改你的代理设置。"
}

func shortenProxy(p string) string {
	if u, err := url.Parse(p); err == nil && u.Host != "" {
		return u.Host
	}
	return p
}

// detectProxy returns the proxy URL found in the environment or desktop
// configuration, "none" when explicitly disabled, or "" when unconfigured.
func detectProxy() string {
	for _, key := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY", "all_proxy", "ALL_PROXY"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	if mode, host := kdeProxy(); mode != "" {
		if mode == "none" || mode == "0" {
			return "none"
		}
		if host != "" {
			return "http://" + host
		}
		return "kde-manual"
	}
	return ""
}

// kdeProxy reads the KDE proxy setting (kioslaverc). It never writes the file.
func kdeProxy() (mode, host string) {
	path := ""
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		path = filepath.Join(v, "kioslaverc")
	} else if home, err := os.UserHomeDir(); err == nil {
		path = filepath.Join(home, ".config", "kioslaverc")
	}
	if path == "" {
		return "", ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	inSection := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inSection = line == "[Proxy Settings]"
			continue
		}
		if !inSection {
			continue
		}
		switch {
		case strings.HasPrefix(line, "ProxyType="):
			mode = strings.TrimSpace(strings.TrimPrefix(line, "ProxyType="))
		case strings.HasPrefix(line, "httpProxy="):
			host = strings.TrimSpace(strings.TrimPrefix(line, "httpProxy="))
			host = strings.TrimPrefix(host, "http://")
		}
	}
	if mode == "0" {
		mode = "none"
	}
	if mode == "1" && host == "" {
		mode = "manual"
	}
	return mode, host
}

func exemptByNoProxy(host string) bool {
	for _, key := range []string{"no_proxy", "NO_PROXY"} {
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		for _, entry := range strings.Split(v, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "*" {
				return true
			}
			if entry == host {
				return true
			}
			if strings.HasPrefix(entry, ".") && strings.HasSuffix(host, entry[1:]) {
				return true
			}
			if _, cidr, err := net.ParseCIDR(entry); err == nil {
				if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
					return true
				}
			}
		}
	}
	return false
}

func exemptByKDENoProxy(host string) bool {
	path := ""
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		path = filepath.Join(v, "kioslaverc")
	} else if home, err := os.UserHomeDir(); err == nil {
		path = filepath.Join(home, ".config", "kioslaverc")
	}
	if path == "" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	inSection := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inSection = line == "[Proxy Settings]"
			continue
		}
		if !inSection || !strings.HasPrefix(line, "NoProxyFor=") {
			continue
		}
		for _, entry := range strings.Split(strings.TrimPrefix(line, "NoProxyFor="), ",") {
			entry = strings.TrimSpace(entry)
			if entry == host {
				return true
			}
			if _, cidr, err := net.ParseCIDR(entry); err == nil {
				if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
					return true
				}
			}
		}
	}
	return false
}
