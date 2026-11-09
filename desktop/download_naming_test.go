package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests only exercise the naming rules, so they run with CGO_ENABLED=0
// -tags purego and on every platform; the GTK/WebKit wiring is covered by the
// container probe described in the task report.

func TestSanitizeDownloadName(t *testing.T) {
	cases := []struct {
		name      string
		suggested string
		want      string
	}{
		{"plain", "same.png", "same.png"},
		{"cjk and space", "报告 2024.pdf", "报告 2024.pdf"},
		{"unix path", "../../etc/passwd", "passwd"},
		{"windows path", `C:\Users\bob\Desktop\a.txt`, "a.txt"},
		{"traversal only in name", "a..b.png", "ab.png"},
		{"dot dot component", "foo/../bar.png", "bar.png"},
		{"control chars", "bad\x00\x01\x1f\x7fname.png", "badname.png"},
		{"surrounding space", "  name.png\t", "name.png"},
		{"leading dot", "....hidden", "hidden"},
		{"empty", "", "download"},
		{"only separators", "///", "download"},
		{"only dots", "...", "download"},
	}
	for _, tc := range cases {
		if got := sanitizeDownloadName(tc.suggested); got != tc.want {
			t.Errorf("%s: sanitizeDownloadName(%q) = %q, want %q", tc.name, tc.suggested, got, tc.want)
		}
	}
}

func TestSanitizeDownloadNameTruncates(t *testing.T) {
	long := strings.Repeat("é", 200) + ".png" // 400+ bytes of name before the extension
	got := sanitizeDownloadName(long)
	if len(got) > downloadNameMaxBytes {
		t.Fatalf("sanitizeDownloadName kept %d bytes, want <= %d", len(got), downloadNameMaxBytes)
	}
	if !strings.HasSuffix(got, ".png") {
		t.Fatalf("sanitizeDownloadName dropped the extension: %q", got)
	}
	if !utf8Valid(got) {
		t.Fatalf("sanitizeDownloadName produced invalid UTF-8: %q", got)
	}

	// A name without an extension is simply cut.
	plain := sanitizeDownloadName(strings.Repeat("a", 400))
	if len(plain) != downloadNameMaxBytes {
		t.Fatalf("len(plain) = %d, want %d", len(plain), downloadNameMaxBytes)
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

func TestNextFreeDownloadPath(t *testing.T) {
	taken := map[string]bool{}
	exists := func(path string) bool { return taken[path] }

	dir := "/tmp/dl"
	first, err := nextFreeDownloadPath(dir, "foo.png", exists)
	if err != nil || first != filepath.Join(dir, "foo.png") {
		t.Fatalf("first = %q, %v", first, err)
	}
	taken[first] = true
	second, err := nextFreeDownloadPath(dir, "foo.png", exists)
	if err != nil || second != filepath.Join(dir, "foo (1).png") {
		t.Fatalf("second = %q, %v", second, err)
	}
	taken[second] = true
	third, err := nextFreeDownloadPath(dir, "foo.png", exists)
	if err != nil || third != filepath.Join(dir, "foo (2).png") {
		t.Fatalf("third = %q, %v", third, err)
	}

	// No extension: the counter goes at the end.
	noExt, err := nextFreeDownloadPath(dir, "foo", func(path string) bool {
		return path == filepath.Join(dir, "foo")
	})
	if err != nil || noExt != filepath.Join(dir, "foo (1)") {
		t.Fatalf("noExt = %q, %v", noExt, err)
	}
}

func TestUniqueDownloadPathNeverTouchesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "same.png")
	if err := os.WriteFile(first, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := uniqueDownloadPath(dir, "same.png")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "same (1).png") {
		t.Fatalf("uniqueDownloadPath = %q, want %q", got, filepath.Join(dir, "same (1).png"))
	}
	if data, err := os.ReadFile(first); err != nil || string(data) != "old" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}

	// A dangling symlink counts as taken as well: writing through it would leave
	// the download directory.
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "link.png")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	got, err = uniqueDownloadPath(dir, "link.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, lerr := os.Lstat(filepath.Join(dir, "link.png")); lerr != nil {
		t.Fatalf("existing symlink disappeared: %v", lerr)
	}
	if got != filepath.Join(dir, "link (1).png") {
		t.Fatalf("uniqueDownloadPath(link.png) = %q, want the free counter name", got)
	}
}

func TestResolveDownloadDir(t *testing.T) {
	home := t.TempDir()

	// No XDG download dir: $HOME/Downloads is created.
	got, err := resolveDownloadDir("", home)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, "Downloads") {
		t.Fatalf("resolveDownloadDir(special=\"\") = %q, want %q", got, filepath.Join(home, "Downloads"))
	}

	// An unusable XDG candidate must not abort: the next candidate is used.
	specialFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(specialFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = resolveDownloadDir(specialFile, home)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, "Downloads") {
		t.Fatalf("resolveDownloadDir(specialFile) = %q, want the $HOME/Downloads fallback", got)
	}

	// A usable XDG download dir wins.
	special := t.TempDir()
	got, err = resolveDownloadDir(special, home)
	if err != nil || got != special {
		t.Fatalf("resolveDownloadDir(special) = %q, %v", got, err)
	}

	// Nothing usable at all: an error, never a silent half state.
	if _, err := resolveDownloadDir("", ""); err == nil {
		t.Fatal("resolveDownloadDir(\"\", \"\") returned no error")
	}
}
