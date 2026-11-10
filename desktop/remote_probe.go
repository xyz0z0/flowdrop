package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"flowdrop/desktop/internal/network"
)

// remoteProbeTimeout is the single deadline of one reachability probe: a Hub on
// the same LAN or on Tailscale answers "/" immediately, so two seconds is long
// enough to tell "not reachable" apart from "slow" without making the save
// button feel stuck.
const remoteProbeTimeout = 2 * time.Second

// probeRemoteHub is the reachability check ApplyMode runs before it saves a
// remote Hub address. It exists only so tests can replace the network probe;
// production code always uses defaultProbeRemoteHub.
var probeRemoteHub = defaultProbeRemoteHub

// defaultProbeRemoteHub asks <remote>/ for an HTTP response. Any 2xx/3xx counts
// as reachable (the Hub serves its inbox there), any 4xx/5xx does not, and a
// network error is returned as-is. The body is drained and discarded: this
// probe must not parse content, keep cookies or otherwise behave like a client.
//
// ctx carries cancellation only; remoteProbeTimeout is the one deadline, so a
// timeout is not hidden behind a second layer.
func defaultProbeRemoteHub(ctx context.Context, baseURL string) error {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: remoteProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("对端返回 HTTP %d", resp.StatusCode)
	}
	return nil
}

// probeTimedOut reports whether err is the probe's own deadline.
func probeTimedOut(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// probeConnFailed reports whether err means the address could not be connected
// to at all (refused, unreachable, no route). A dial failure is a *net.OpError;
// timeouts are classified before this check.
func probeConnFailed(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// remoteProbeFailureMessage turns a failed probe into the text the settings
// window shows. It names the address that was tried, classifies the reason and
// tells the user which address to use instead, because the two most common
// causes here are "the Hub is not running / the port is closed" and "both
// machines use 192.168.x.x but are not on the same physical LAN".
func remoteProbeFailureMessage(baseURL string, err error) string {
	reason := ""
	switch {
	case probeTimedOut(err):
		reason = fmt.Sprintf("%d 秒内没有响应（超时）", int(remoteProbeTimeout/time.Second))
	case probeConnFailed(err):
		reason = "无法建立连接（对端可能没有启动 Hub，或端口/防火墙没开放）"
	default:
		reason = err.Error()
	}
	return fmt.Sprintf(
		"无法连接远程 Hub %s：%s。"+
			"如果对端在 Tailscale 上，请改填它的 100.64.0.0/10 地址（例如 http://100.x.y.z:8080）；"+
			"如果两台机器各自使用 192.168.x.x 网段但不在同一个局域网，LAN 地址会不可达。"+
			"运行模式与保存的配置没有改变。",
		baseURL, reason,
	)
}

// remoteSubnetConflict warns when the remote Hub address lives in the same
// IPv4 /24 as one of this machine's real addresses. That is a hint, not a
// judgement: the same /24 may be a genuinely shared LAN, but two different
// physical networks that both use 192.168.x.x look exactly like this. Tailscale
// (100.64.0.0/10) addresses have no such trap, so they never warn, and neither
// do host names, IPv6 or public addresses.
func remoteSubnetConflict(targetURL string, local []network.Interface) string {
	u, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	target := net.ParseIP(host)
	if target == nil {
		// A host name (flowdrop.local, example.com) says nothing about ranges;
		// resolving it could pick an unrelated address.
		return ""
	}
	target4 := target.To4()
	if target4 == nil || !network.IsLAN(target4) {
		// IPv6, Tailscale/CGNAT and public addresses have no private-range
		// collision to report.
		return ""
	}
	for _, i := range local {
		if i.Kind == network.KindLoopback {
			continue
		}
		ip := net.ParseIP(i.Address)
		local4 := ip.To4()
		if local4 == nil || local4.IsLoopback() || local4.IsLinkLocalUnicast() {
			continue
		}
		if local4[0] != target4[0] || local4[1] != target4[1] || local4[2] != target4[2] {
			continue
		}
		// Only the first match is reported: more than one local address in that
		// /24 is the same warning, and the caller's order is what the window
		// shows elsewhere.
		return fmt.Sprintf(
			"疑似同网段冲突：远程 Hub %s 与本机地址 %s 都在 %d.%d.%d.0/24 网段。"+
				"这可能确实是同一个局域网，也可能是两台机器各自使用相同的 192.168.x.x 网段；"+
				"若不在同一个局域网，请改用对端的 Tailscale 100.x 地址。",
			target4.String(), local4.String(), target4[0], target4[1], target4[2],
		)
	}
	return ""
}
