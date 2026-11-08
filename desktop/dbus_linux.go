//go:build linux

package main

import (
	"log"

	"github.com/godbus/dbus/v5"
)

// dbusWatcherPresent reports whether the session bus has a StatusNotifier
// watcher. KDE Plasma provides org.kde.StatusNotifierWatcher; GNOME needs an
// extension that provides the same name. Without a watcher the Wails tray
// silently fails to appear, so the shell must not rely on it.
// trayWatcherPresent reports whether this desktop session has a StatusNotifier
// watcher (KDE Plasma/GNOME with an extension). Without it the Wails tray
// cannot be shown, so the shell switches to the confirm-quit fallback.
func trayWatcherPresent() bool { return dbusWatcherPresent() }

func dbusWatcherPresent() bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		log.Printf("no D-Bus session bus (%v): tray unavailable", err)
		return false
	}
	defer conn.Close()
	for _, name := range []string{
		"org.kde.StatusNotifierWatcher",
		"org.freedesktop.StatusNotifierWatcher",
		"org.ayatana.indicator.application",
	} {
		var has bool
		if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&has); err == nil && has {
			return true
		}
	}
	return false
}
