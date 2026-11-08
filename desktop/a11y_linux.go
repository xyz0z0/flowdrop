//go:build linux

package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// a11yProbeTimeout bounds the startup accessibility-bus health check. A healthy
// session bus and AT-SPI registry answer in a few milliseconds; only a broken
// or overloaded one makes the app wait this long, and only such a session is
// subject to the crash this guard prevents.
const a11yProbeTimeout = 2 * time.Second

// guardAccessibilityBridge decides, before any GTK/WebKit initialisation,
// whether this process may keep the AT-SPI accessibility bridge enabled.
//
// Background: libatk-bridge 2.60.4 dereferences a NULL spi_global_app_data in
// spi_register_object_to_path() when the session's accessibility bus is broken
// (registration fails), and WebKitGTK still registers accessible objects while
// creating its WebView. That produced a native SIGSEGV on the GTK main loop
// that Go cannot recover from, in a session whose AT-SPI bus had accumulated
// orphaned daemons. The crash is not caused by FlowDrop code, so the only
// safe in-process mitigation is not to load the broken bridge.
//
// Policy:
//
//   - FLOWDROP_DESKTOP_A11Y=on|keep: never probe and never disable; the user
//     explicitly keeps accessibility (and accepts the risk).
//   - FLOWDROP_DESKTOP_A11Y=off|none: disable the bridge for this launch.
//   - NO_AT_BRIDGE already set: respect the caller, do not override it.
//   - otherwise (default "auto"): probe the AT-SPI registry once. Only when it
//     fails to answer does this process set NO_AT_BRIDGE=1 for itself, with a
//     clear diagnosis. The system accessibility configuration is never touched.
func guardAccessibilityBridge() {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOWDROP_DESKTOP_A11Y"))) {
	case "on", "keep":
		log.Printf("accessibility bridge: kept (FLOWDROP_DESKTOP_A11Y=on); not probing the AT-SPI bus")
		return
	case "off", "none":
		disableAccessibilityBridge("FLOWDROP_DESKTOP_A11Y=off")
		return
	}
	if v := os.Getenv("NO_AT_BRIDGE"); v != "" {
		log.Printf("accessibility bridge: NO_AT_BRIDGE=%q already set; not probing the AT-SPI bus", v)
		return
	}
	ok, reason := a11yBusProbe(a11yProbeTimeout)
	if ok {
		log.Printf("accessibility bridge: AT-SPI bus responsive; accessibility left enabled")
		return
	}
	disableAccessibilityBridge(reason)
}

// disableAccessibilityBridge disables the ATK bridge for this process only and
// records the diagnosis for the settings window. It must run before GTK reads
// NO_AT_BRIDGE (i.e. before app.Run initialises GTK).
func disableAccessibilityBridge(reason string) {
	_ = os.Setenv("NO_AT_BRIDGE", "1")
	desktop.a11yFallback.Store(reason)
	log.Printf(
		"accessibility bridge disabled for this launch: %s\n"+
			"  the session's AT-SPI accessibility bus is not answering; the broken "+
			"at-spi bridge would crash WebKitGTK when the first window is created.\n"+
			"  only this process is affected (NO_AT_BRIDGE=1); screen readers cannot "+
			"read FlowDrop until the session bus is healthy again.\n"+
			"  system accessibility settings are unchanged. Log out and back in (or "+
			"repair the session's at-spi bus) to get accessibility back; "+
			"FLOWDROP_DESKTOP_A11Y=on keeps the bridge at your own risk.",
		reason,
	)
}

// a11yBusProbe checks that the session's accessibility bus and its registry
// answer a real D-Bus round trip. It is wrapped in a goroutine with a hard
// deadline so a wedged connection cannot hold up the application start.
func a11yBusProbe(timeout time.Duration) (bool, string) {
	type result struct {
		ok     bool
		reason string
	}
	done := make(chan result, 1)
	go func() {
		ok, reason := probeA11yBus(timeout)
		done <- result{ok: ok, reason: reason}
	}()
	select {
	case r := <-done:
		return r.ok, r.reason
	case <-time.After(timeout + time.Second):
		return false, "AT-SPI 无障碍总线在 " + timeout.String() + " 内没有完成响应"
	}
}

// probeA11yBus connects to the accessibility bus and calls the read-only
// org.a11y.atspi.Registry.GetRegisteredEvents method. Any missing socket,
// broken connection, unresponsive bus daemon or unresponsive registry shows up
// as a failed round trip.
func probeA11yBus(timeout time.Duration) (bool, string) {
	address, reason := a11yBusAddress(timeout)
	if address == "" {
		return false, reason
	}
	network, target, err := a11yUnixTarget(address)
	if err != nil {
		return false, err.Error()
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial(network, target)
	if err != nil {
		return false, "无法连接 AT-SPI 无障碍总线（" + address + "）：" + err.Error()
	}
	defer conn.Close()

	// Auth, Hello and the registry call each need the full budget; extend the
	// socket deadline before every phase so a slow phase cannot make the next
	// one fail early for the wrong reason.
	_ = conn.SetDeadline(time.Now().Add(timeout))
	bus, err := dbus.NewConn(conn)
	if err != nil {
		return false, "无法使用 AT-SPI 无障碍总线连接：" + err.Error()
	}
	if err := bus.Auth(nil); err != nil {
		return false, "AT-SPI 无障碍总线认证失败：" + err.Error()
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := bus.Hello(); err != nil {
		return false, "AT-SPI 无障碍总线没有完成 D-Bus Hello：" + err.Error()
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	registry := bus.Object("org.a11y.atspi.Registry", dbus.ObjectPath("/org/a11y/atspi/registry"))
	call := registry.GoWithContext(ctx, "org.a11y.atspi.Registry.GetRegisteredEvents", 0, make(chan *dbus.Call, 1))
	select {
	case c := <-call.Done:
		if c.Err != nil {
			return false, "AT-SPI 无障碍注册服务没有响应：" + c.Err.Error()
		}
		return true, ""
	case <-ctx.Done():
		return false, "AT-SPI 无障碍注册服务在 " + timeout.String() + " 内无响应"
	}
}

// a11yBusAddress returns the accessibility bus address dbind itself would use:
// AT_SPI_BUS_ADDRESS when set, otherwise org.a11y.Bus.GetAddress from the
// session bus.
func a11yBusAddress(timeout time.Duration) (string, string) {
	if address := strings.TrimSpace(os.Getenv("AT_SPI_BUS_ADDRESS")); address != "" {
		return address, ""
	}
	// A short timeout keeps a wedged session bus from delaying startup; the
	// caller's goroutine deadline is the final safety net.
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return "", "没有可用的 D-Bus 会话总线：" + err.Error()
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	a11yBus := conn.Object("org.a11y.Bus", dbus.ObjectPath("/org/a11y/bus"))
	call := a11yBus.GoWithContext(ctx, "org.a11y.Bus.GetAddress", 0, make(chan *dbus.Call, 1))
	select {
	case c := <-call.Done:
		if c.Err != nil {
			return "", "无法取得 AT-SPI 无障碍总线地址：" + c.Err.Error()
		}
		var address string
		if err := c.Store(&address); err != nil {
			return "", "无法解析 AT-SPI 无障碍总线地址：" + err.Error()
		}
		address = strings.TrimSpace(address)
		if address == "" {
			return "", "AT-SPI 无障碍总线地址为空"
		}
		return address, ""
	case <-ctx.Done():
		return "", "org.a11y.Bus.GetAddress 在 " + timeout.String() + " 内无响应"
	}
}

// a11yUnixTarget turns a D-Bus unix address (unix:path=... or
// unix:abstract=...) into the arguments net.Dial needs. Other transports are
// rejected rather than guessed at.
func a11yUnixTarget(address string) (network, target string, err error) {
	for _, prefix := range []string{"unix:path=", "unix:abstract="} {
		if !strings.HasPrefix(address, prefix) {
			continue
		}
		value := strings.TrimPrefix(address, prefix)
		if i := strings.IndexByte(value, ','); i >= 0 {
			value = value[:i]
		}
		if value == "" {
			return "", "", errors.New("AT-SPI 无障碍总线地址缺少路径：" + address)
		}
		if prefix == "unix:abstract=" {
			// Linux abstract sockets are addressed with a leading '@'.
			return "unix", "@" + value, nil
		}
		return "unix", value, nil
	}
	return "", "", errors.New("无法识别的 AT-SPI 无障碍总线地址：" + address)
}
