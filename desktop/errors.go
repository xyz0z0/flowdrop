package main

import "flowdrop/pkg/hub"

// errAdminExists exposes the Hub's sentinel so the settings window can explain
// why a password change was refused without string matching.
func errAdminExists() error { return hub.ErrAdminExists }
