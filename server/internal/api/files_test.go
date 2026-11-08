package api

import (
	"net/url"
	"strings"
	"testing"
)

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"photo.png":              "photo.png",
		"../../etc/passwd":       "passwd",
		`C:\Users\bob\a.txt`:     "a.txt",
		"/absolute/path/x.pdf":   "x.pdf",
		"../":                    "file",
		"..":                     "file",
		".":                      "file",
		"":                       "file",
		"   ":                    "file",
		"a\x00b\x1fc.txt":        "abc.txt",
		"截图 2026-11-07.png":      "截图 2026-11-07.png",
		"app-debug.apk":          "app-debug.apk",
		"trailing.space.  ":      "trailing.space.",
		"...hidden":              "hidden",
		"line\nbreak.txt":        "linebreak.txt",
		"../../../../../../tmp/": "tmp",
	}
	for in, want := range cases {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeFileNameTruncatesLongNames(t *testing.T) {
	long := strings.Repeat("A", 300) + ".png"
	got := sanitizeFileName(long)
	if n := len([]rune(got)); n > maxNameLen {
		t.Fatalf("length %d exceeds %d", n, maxNameLen)
	}
	if !strings.HasSuffix(got, ".png") {
		t.Fatalf("extension lost: %q", got)
	}
}

func TestClassifyUpload(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 16))
	jpeg := []byte("\xff\xd8\xff\xe0" + strings.Repeat("\x00", 16))
	gif := []byte("GIF89a" + strings.Repeat("\x00", 16))
	html := []byte("<html><body>x</body></html>")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>1</script></svg>`)

	cases := []struct {
		name, ctype, itemType string
		head                  []byte
	}{
		{"a.png", "image/png", "image", png},
		{"a.jpg", "image/jpeg", "image", jpeg},
		{"a.gif", "image/gif", "image", gif},
		// An active-content extension is never previewable even if the bytes
		// sniff as an image or text.
		{"a.svg", "application/octet-stream", "file", svg},
		{"a.html", "application/octet-stream", "file", html},
		{"a.htm", "application/octet-stream", "file", html},
		// A .png extension cannot launder HTML into an image, and the markup
		// sniff forces an opaque type.
		{"evil.png", "application/octet-stream", "file", html},
		// A zip/APK sniffs as application/zip: honest, and never previewable.
		{"app.apk", "application/zip", "file", []byte("PK\x03\x04")},
		{"notes.txt", "text/plain", "file", []byte("hello")},
		{"noext", "application/octet-stream", "file", []byte{}},
	}
	for _, c := range cases {
		ct, it := classifyUpload(c.name, c.head)
		if ct != c.ctype || it != c.itemType {
			t.Errorf("classifyUpload(%q) = (%q,%q), want (%q,%q)", c.name, ct, it, c.ctype, c.itemType)
		}
	}
}

func TestContentDisposition(t *testing.T) {
	got := contentDisposition("截图 2026.png", true)
	if !strings.HasPrefix(got, "inline; ") {
		t.Fatalf("prefix = %q", got)
	}
	if !strings.Contains(got, `filename="`) {
		t.Fatalf("missing ascii fallback: %q", got)
	}
	want := "filename*=UTF-8''" + url.PathEscape("截图 2026.png")
	if !strings.Contains(got, want) {
		t.Fatalf("missing RFC 5987 form %q in %q", want, got)
	}
	// No raw quotes or newlines can break out of the header.
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("header injection characters present: %q", got)
	}
	if d := contentDisposition("a\"b.txt", false); !strings.HasPrefix(d, "attachment; ") {
		t.Fatalf("attachment prefix = %q", d)
	}
}

func TestValidHTTPURL(t *testing.T) {
	good := []string{
		"https://example.com",
		"https://example.com/path?q=1#frag",
		"http://192.168.1.5:8080/x",
	}
	for _, s := range good {
		if !validHTTPURL(s) {
			t.Errorf("rejected valid URL %q", s)
		}
	}
	bad := []string{
		"javascript:alert(1)",
		"file:///etc/passwd",
		"data:text/html,<script>",
		"//example.com",
		"example.com",
		"https://",
		"https://exa mple.com",
		"https://example.com/\nheader",
		"",
		strings.Repeat("https://example.com/", 500),
	}
	for _, s := range bad {
		if validHTTPURL(s) {
			t.Errorf("accepted invalid URL %q", s)
		}
	}
}

func TestSanitizeSender(t *testing.T) {
	if got := sanitizeSender("  Kubuntu 桌面 \n"); got != "Kubuntu 桌面" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeSender(strings.Repeat("x", 200)); len([]rune(got)) != maxSenderLen {
		t.Fatalf("length = %d", len([]rune(got)))
	}
	if got := sanitizeSender("a\x00b"); got != "ab" {
		t.Fatalf("control chars kept: %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{
		512:       "512 B",
		1024:      "1 KiB",
		1536:      "2 KiB", // rounds to nearest
		1 << 20:   "1 MiB",
		512 << 20: "512 MiB",
		3 << 30:   "3 GiB",
	} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
