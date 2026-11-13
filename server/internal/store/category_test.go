package store

import (
	"fmt"
	"testing"
	"time"
)

func TestCategoryOf(t *testing.T) {
	cases := []struct {
		name     string
		itemType string
		blob     *Blob
		want     Category
	}{
		// Original item types win.
		{"text", "text", nil, CategoryText},
		{"link", "link", nil, CategoryLink},
		{"image item", "image", &Blob{OriginalName: "a.bin", ContentType: "application/octet-stream"}, CategoryImage},

		// Extension wins over MIME.
		{"png", "file", &Blob{OriginalName: "a.png", ContentType: "application/octet-stream"}, CategoryImage},
		{"mp4 over octet-stream", "file", &Blob{OriginalName: "clip.mp4", ContentType: "application/octet-stream"}, CategoryVideo},
		{"mkv not webm", "file", &Blob{OriginalName: "clip.mkv", ContentType: "video/webm"}, CategoryVideo},
		{"flac not octet-stream", "file", &Blob{OriginalName: "a.flac", ContentType: "application/octet-stream"}, CategoryAudio},
		{"7z not octet-stream", "file", &Blob{OriginalName: "a.7z", ContentType: "application/octet-stream"}, CategoryArchive},
		{"old doc not octet-stream", "file", &Blob{OriginalName: "a.doc", ContentType: "application/octet-stream"}, CategoryDocument},
		{"docx beats zip MIME", "file", &Blob{OriginalName: "a.docx", ContentType: "application/zip"}, CategoryDocument},
		{"xlsx beats zip MIME", "file", &Blob{OriginalName: "a.xlsx", ContentType: "application/zip"}, CategoryDocument},
		{"pptx beats zip MIME", "file", &Blob{OriginalName: "a.pptx", ContentType: "application/zip"}, CategoryDocument},
		{"apk forced other over zip", "file", &Blob{OriginalName: "app.apk", ContentType: "application/zip"}, CategoryOther},
		{"jar forced other over zip", "file", &Blob{OriginalName: "lib.jar", ContentType: "application/zip"}, CategoryOther},
		{"iso forced other", "file", &Blob{OriginalName: "disk.iso", ContentType: "application/octet-stream"}, CategoryOther},
		{"exe forced other", "file", &Blob{OriginalName: "setup.exe", ContentType: "application/vnd.microsoft.portable-executable"}, CategoryOther},
		{"upper-case extension", "file", &Blob{OriginalName: "SHOT.PNG", ContentType: "application/octet-stream"}, CategoryImage},
		{"mixed-case extension", "file", &Blob{OriginalName: "Mix.Mp4", ContentType: "application/octet-stream"}, CategoryVideo},

		// MIME fallback when the extension is unknown.
		{"mime video", "file", &Blob{OriginalName: "blob", ContentType: "video/quicktime"}, CategoryVideo},
		{"mime audio with params", "file", &Blob{OriginalName: "blob", ContentType: "audio/ogg; codecs=opus"}, CategoryAudio},
		{"mime upper-case", "file", &Blob{OriginalName: "blob", ContentType: "IMAGE/PNG"}, CategoryImage},
		{"mime text", "file", &Blob{OriginalName: "blob", ContentType: "text/plain; charset=utf-8"}, CategoryDocument},
		{"mime pdf", "file", &Blob{OriginalName: "blob", ContentType: "application/pdf"}, CategoryDocument},
		{"mime openxml", "file", &Blob{OriginalName: "blob", ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, CategoryDocument},
		{"mime zip", "file", &Blob{OriginalName: "blob", ContentType: "application/zip"}, CategoryArchive},
		{"mime gzip", "file", &Blob{OriginalName: "blob", ContentType: "application/gzip"}, CategoryArchive},

		// Unknown extension and octet-stream MIME.
		{"unknown octet-stream", "file", &Blob{OriginalName: "mystery", ContentType: "application/octet-stream"}, CategoryOther},
		{"unknown extension", "file", &Blob{OriginalName: "mystery.xyzzy", ContentType: "application/octet-stream"}, CategoryOther},
		{"no blob", "file", nil, CategoryOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CategoryOf(c.itemType, c.blob); got != c.want {
				t.Fatalf("CategoryOf(%q, %+v) = %q, want %q", c.itemType, c.blob, got, c.want)
			}
		})
	}
}

func TestParseCategory(t *testing.T) {
	for _, c := range Categories() {
		if got, ok := ParseCategory(string(c)); !ok || got != c {
			t.Errorf("ParseCategory(%q) = %q, %v", c, got, ok)
		}
	}
	for _, bad := range []string{"", "ALL", "Text", "docs", "all ", "file", "unknown"} {
		if _, ok := ParseCategory(bad); ok {
			t.Errorf("ParseCategory(%q) accepted", bad)
		}
	}
}

func TestListItemsByCategoryPaginatesMixedData(t *testing.T) {
	s, conn := newTestStore(t)

	// 36 items: 4 per category, cycling, over 12 timestamps (3 items each).
	specs := []struct {
		itemType string
		name     string
		ctype    string
		want     Category
	}{
		{"text", "", "", CategoryText},
		{"link", "", "", CategoryLink},
		{"image", "photo.png", "image/png", CategoryImage},
		{"file", "clip.mp4", "video/mp4", CategoryVideo},
		{"file", "song.flac", "application/octet-stream", CategoryAudio},
		{"file", "notes.docx", "application/zip", CategoryDocument},
		{"file", "bundle.zip", "application/zip", CategoryArchive},
		{"file", "app.apk", "application/zip", CategoryOther},
		{"file", "mystery", "application/octet-stream", CategoryOther},
	}
	base := time.Now().UnixMilli() - 500_000
	want := map[Category][]string{}
	var allIDs []string
	for i := 0; i < 36; i++ {
		spec := specs[i%len(specs)]
		id := fmt.Sprintf("item-%02d", i)
		ts := base + int64(i/3)*1000 // every three items share a timestamp
		if spec.name == "" {
			if _, err := conn.Exec(`INSERT INTO inbox_items(id, type, text_content, sender_device, created_at)
				VALUES(?,?,?,?,?)`, id, spec.itemType, "body-"+id, "", ts); err != nil {
				t.Fatal(err)
			}
		} else {
			blobID := "blob-" + id
			if _, err := conn.Exec(`INSERT INTO blobs(id, original_name, content_type, size_bytes, sha256, storage_key, created_at)
				VALUES(?,?,?,?,?,?,?)`, blobID, spec.name, spec.ctype, 4, "sha", "key-"+id, ts); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(`INSERT INTO inbox_items(id, type, blob_id, sender_device, created_at)
				VALUES(?,?,?,?,?)`, id, spec.itemType, blobID, "", ts); err != nil {
				t.Fatal(err)
			}
		}
		want[spec.want] = append(want[spec.want], id)
		allIDs = append(allIDs, id)
	}

	collect := func(category Category, limit int) []Item {
		t.Helper()
		var got []Item
		var cursor *Cursor
		for pages := 0; ; pages++ {
			if pages > 100 {
				t.Fatalf("%s: pagination did not terminate", category)
			}
			items, next, err := s.ListItemsByCategory(cursor, category, limit)
			if err != nil {
				t.Fatalf("%s: %v", category, err)
			}
			if len(items) > limit {
				t.Fatalf("%s: page returned %d items, limit %d", category, len(items), limit)
			}
			got = append(got, items...)
			if next == nil {
				return got
			}
			cursor = next
		}
	}

	// Each category is reachable regardless of page size, with no duplicates
	// even though items share timestamps.
	for _, category := range Categories() {
		for _, limit := range []int{1, 2, 3, 5, 100} {
			got := collect(category, limit)
			expected := allIDs
			if category != CategoryAll {
				expected = want[category]
			}
			if len(got) != len(expected) {
				t.Fatalf("%s limit=%d: got %d items, want %d", category, limit, len(got), len(expected))
			}
			seen := map[string]bool{}
			for _, it := range got {
				if seen[it.ID] {
					t.Fatalf("%s limit=%d: duplicate %s", category, limit, it.ID)
				}
				seen[it.ID] = true
				if category != CategoryAll && it.Category != category {
					t.Fatalf("%s limit=%d: item %s has category %s", category, limit, it.ID, it.Category)
				}
			}
			for _, id := range expected {
				if !seen[id] {
					t.Fatalf("%s limit=%d: missing %s", category, limit, id)
				}
			}
		}
	}
}

func TestItemCategoryIsSetOnEveryWritePath(t *testing.T) {
	s, _ := newTestStore(t)

	text, err := s.CreateTextItem(NewID(), "link", "https://example.com/x", "d")
	if err != nil {
		t.Fatal(err)
	}
	if text.Category != CategoryLink {
		t.Fatalf("created link category = %q", text.Category)
	}

	blob := Blob{ID: "b-doc", OriginalName: "report.docx", ContentType: "application/zip",
		SizeBytes: 3, SHA256: "s", StorageKey: "55555555555555555555555555555555"}
	created, err := s.CreateBlobItem(blob, "file", "d")
	if err != nil {
		t.Fatal(err)
	}
	if created.Category != CategoryDocument {
		t.Fatalf("created blob category = %q", created.Category)
	}

	got, err := s.GetItem(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Category != CategoryDocument {
		t.Fatalf("GetItem category = %q", got.Category)
	}

	listed, _, err := s.ListItems(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d items", len(listed))
	}
	for _, it := range listed {
		if it.Category == "" {
			t.Fatalf("item %s has empty category", it.ID)
		}
	}
}
