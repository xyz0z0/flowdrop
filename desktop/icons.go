package main

import _ "embed"

// Icons are embedded so the launcher, the window and the tray work from any
// working directory and after `install-desktop.sh` copies just the binary.
//
//go:embed assets/icon.png
var iconPNG []byte

//go:embed assets/tray.png
var trayPNG []byte
