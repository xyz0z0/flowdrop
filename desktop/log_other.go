//go:build !windows

package main

import "io"

// openLogFile is a no-op outside Windows: those builds are started from a
// terminal or a journal that already captures stderr, so no extra sink is
// wanted. A nil file means "keep logging to stderr only".
func openLogFile() (io.WriteCloser, error) {
	return nil, nil
}
