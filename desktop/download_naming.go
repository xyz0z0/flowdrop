package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Everything in this file turns an untrusted filename suggestion into exactly
// one free path component inside the download directory. WebKit's
// suggested_filename comes from the response (Content-Disposition, or the URL),
// so a Hub or a peer may put path separators, "..", control characters or an
// over-long name into it, and the destination must never be an existing file:
// WebKit's own default fails and damages a same-name file instead of picking a
// new name.
//
// Both helpers are pure Go (no cgo) so the naming rules are testable with
// -tags purego on any platform.
const (
	// downloadNameMaxBytes keeps the whole name (extension included) below the
	// 255-byte limit of the common Linux filesystems even when the directory
	// part is long.
	downloadNameMaxBytes = 180
	// downloadNameFallback is used when nothing usable is left after cleaning.
	downloadNameFallback = "download"
	// downloadNameMaxAttempts bounds the " (n)" search so a directory full of
	// thousands of same-name files cannot spin here.
	downloadNameMaxAttempts = 10000
)

// sanitizeDownloadName reduces a suggested filename to a single safe component:
// the last path component of either separator style, with "..", control
// characters, surrounding whitespace and leading dots removed, and the length
// cut to downloadNameMaxBytes while the extension is kept.
func sanitizeDownloadName(suggested string) string {
	name := suggested
	// The suggestion may be a URL path or a Windows-style path, so both
	// separators are treated as separators. Only the last component can become
	// a name inside the download directory.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	// ".." is the one component that could make a suggestion address a
	// directory outside the download directory; it never survives.
	for strings.Contains(name, "..") {
		name = strings.ReplaceAll(name, "..", "")
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	// A leading dot would hide the file in the user's file manager.
	name = strings.TrimSpace(name)
	name = strings.TrimLeft(name, ".")
	name = strings.TrimSpace(name)
	if name == "" {
		return downloadNameFallback
	}
	return truncateDownloadName(name)
}

// truncateDownloadName cuts name to downloadNameMaxBytes on a UTF-8 boundary
// and keeps the extension, so "image.png" stays openable as a PNG.
func truncateDownloadName(name string) string {
	if len(name) <= downloadNameMaxBytes {
		return name
	}
	ext := filepath.Ext(name)
	// A "extension" that eats half the budget is not an extension.
	if len(ext) >= downloadNameMaxBytes/2 {
		ext = ""
	}
	stem := name[:len(name)-len(ext)]
	budget := downloadNameMaxBytes - len(ext)
	for len(stem) > budget {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	if stem == "" {
		return downloadNameFallback
	}
	return stem + ext
}

// nextFreeDownloadPath returns dir/name when nothing is there yet, otherwise
// dir/"name (1)".ext, "name (2)".ext, … The exists callback answers for one
// candidate path; it is only ever consulted, never modified, so an existing
// file is never overwritten or removed.
func nextFreeDownloadPath(dir, name string, exists func(string) bool) (string, error) {
	first := filepath.Join(dir, name)
	if !exists(first) {
		return first, nil
	}
	ext := filepath.Ext(name)
	stem := name[:len(name)-len(ext)]
	for n := 1; n <= downloadNameMaxAttempts; n++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, ext))
		if !exists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free file name for %q after %d attempts", name, downloadNameMaxAttempts)
}

// uniqueDownloadPath is nextFreeDownloadPath against the real filesystem.
// os.Lstat is used so a dangling symlink also counts as taken: writing through
// it would put the bytes outside the download directory.
func uniqueDownloadPath(dir, name string) (string, error) {
	return nextFreeDownloadPath(dir, name, func(path string) bool {
		_, err := os.Lstat(path)
		return err == nil || !errors.Is(err, fs.ErrNotExist)
	})
}

// resolveDownloadDir returns an existing, writable directory for downloads:
// WebKit's XDG download directory when the desktop defines one, otherwise
// $HOME/Downloads, otherwise $HOME. Missing directories are created; a
// candidate that cannot be created or written is skipped instead of aborting,
// and nothing outside these candidates is ever touched.
func resolveDownloadDir(special, home string) (string, error) {
	candidates := make([]string, 0, 3)
	if special != "" {
		candidates = append(candidates, special)
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, "Downloads"), home)
	}
	var lastErr error
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			lastErr = err
			continue
		}
		if !dirWritable(dir) {
			lastErr = fmt.Errorf("%s is not writable", dir)
			continue
		}
		return dir, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no download directory candidate (empty XDG download dir and $HOME)")
	}
	return "", lastErr
}

// dirWritable proves write access by creating and removing one probe file:
// a read-only mount or a directory owned by another user looks fine to stat.
func dirWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".flowdrop-download-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		os.Remove(name)
		return false
	}
	return os.Remove(name) == nil
}
