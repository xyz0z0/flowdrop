//go:build !windows

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// installInjectedJS is a no-op outside Windows: on Linux and macOS Wails
// evaluates WebviewWindowOptions.JS itself after every page load, so the same
// scripts are already injected and re-injecting them here would only run them
// twice.
func installInjectedJS(*application.WebviewWindow, string) {}
