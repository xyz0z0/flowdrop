package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// listByCategory requests one page of a category and returns the items plus the
// next cursor.
func (h *harness) listByCategory(category, cursor string, limit int) ([]Item, string) {
	h.t.Helper()
	path := fmt.Sprintf("/api/v1/items?limit=%d&category=%s", limit, url.QueryEscape(category))
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	res := h.do(http.MethodGet, path, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		h.t.Fatalf("list category=%s status = %d body=%s", category, res.StatusCode, b)
	}
	var page struct {
		Items []Item `json:"items"`
		Next  string `json:"next_cursor"`
	}
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		h.t.Fatalf("decode list: %v", err)
	}
	return page.Items, page.Next
}

// collectCategory walks every page of a category with the given page size.
func (h *harness) collectCategory(category string, limit int) []Item {
	h.t.Helper()
	var got []Item
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 200 {
			h.t.Fatalf("category %s: pagination did not terminate", category)
		}
		items, next := h.listByCategory(category, cursor, limit)
		if len(items) > limit {
			h.t.Fatalf("category %s: page returned %d items, limit %d", category, len(items), limit)
		}
		got = append(got, items...)
		if next == "" {
			return got
		}
		cursor = next
	}
}

func TestListItemsCategoryRejectsUnknownValue(t *testing.T) {
	h := newHarness(t)
	h.pair()

	for _, q := range []string{"?category=", "?category=ALL", "?category=Text", "?category=docs", "?category=file"} {
		res := h.do(http.MethodGet, "/api/v1/items"+q, nil)
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if q == "?category=" {
			// An empty value is "not supplied" and keeps the default behavior.
			if res.StatusCode != http.StatusOK {
				t.Errorf("GET /items%s = %d, want 200", q, res.StatusCode)
			}
			continue
		}
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /items%s = %d body=%s, want 400", q, res.StatusCode, raw)
			continue
		}
		if code := decodePairError(t, raw); code != "bad_request" {
			t.Errorf("GET /items%s error = %q, want bad_request", q, code)
		}
	}
}

func TestListItemsCategoryDefaultMatchesLegacyBehavior(t *testing.T) {
	h := newHarness(t)
	h.pair()

	var want []string
	for i := 0; i < 4; i++ {
		it := h.createText("text", fmt.Sprintf("t-%d", i), "d")
		want = append(want, it.ID)
	}

	legacy, _ := h.list("", 10)
	explicitAll, _ := h.listByCategory("all", "", 10)
	if len(legacy) != len(want) || len(explicitAll) != len(want) {
		t.Fatalf("legacy=%d explicit-all=%d, want %d", len(legacy), len(explicitAll), len(want))
	}
	for i := range legacy {
		if legacy[i].ID != explicitAll[i].ID {
			t.Fatalf("position %d: legacy %s vs all %s", i, legacy[i].ID, explicitAll[i].ID)
		}
	}
	// The legacy response shape is unchanged apart from the added field.
	for _, it := range legacy {
		if string(it.Category) != "text" {
			t.Fatalf("legacy item %s category = %q", it.ID, it.Category)
		}
	}
}

func TestListItemsCategoryFiltersAndPaginatesMixedData(t *testing.T) {
	h := newHarness(t)
	h.pair()

	// 36 items across every category: text, link, image, video, audio,
	// document, archive and other. Several share the same created_at
	// millisecond, which the keyset cursor must still handle.
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 40))
	zipBody := []byte("PK\x03\x04" + strings.Repeat("\x00", 40))
	flacBody := []byte("fLaC" + strings.Repeat("\x00", 40))
	mp4Body := []byte("\x00\x00\x00\x18ftypisom" + strings.Repeat("\x00", 40))
	apkBody := []byte("PK\x03\x04" + strings.Repeat("\x01", 40))

	uploads := []struct {
		name  string
		body  []byte
		want  string
		count int
	}{
		{"shot.png", png, "image", 4},
		{"clip.mp4", mp4Body, "video", 4},
		{"song.flac", flacBody, "audio", 4},
		{"report.docx", zipBody, "document", 4},
		{"bundle.zip", zipBody, "archive", 4},
		{"app.apk", apkBody, "other", 4},
		{"mystery", []byte{0x00, 0x01, 0x02, 0x03}, "other", 4},
	}
	want := map[string]map[string]bool{}
	addWant := func(category, id string) {
		if want[category] == nil {
			want[category] = map[string]bool{}
		}
		want[category][id] = true
	}
	allIDs := map[string]bool{}

	for i := 0; i < 4; i++ {
		text := h.createText("text", fmt.Sprintf("note-%d", i), "d")
		addWant("text", text.ID)
		allIDs[text.ID] = true
		link := h.createText("link", fmt.Sprintf("https://example.com/%d", i), "d")
		addWant("link", link.ID)
		allIDs[link.ID] = true
		for _, up := range uploads {
			res, body := h.upload(up.name, up.body, "d")
			if res.StatusCode != http.StatusCreated {
				t.Fatalf("%s upload = %d body=%s", up.name, res.StatusCode, body)
			}
			it := decodeItem(t, body)
			addWant(up.want, it.ID)
			allIDs[it.ID] = true
		}
	}
	if len(allIDs) != 36 {
		t.Fatalf("seeded %d items, want 36", len(allIDs))
	}

	categories := []string{"all", "text", "link", "image", "video", "audio", "document", "archive", "other"}
	for _, category := range categories {
		for _, limit := range []int{1, 3, 7, 100} {
			got := h.collectCategory(category, limit)
			expected := allIDs
			if category != "all" {
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
				if category != "all" && string(it.Category) != category {
					t.Fatalf("%s limit=%d: item %s has category %q", category, limit, it.ID, it.Category)
				}
			}
			for id := range expected {
				if !seen[id] {
					t.Fatalf("%s limit=%d: missing %s", category, limit, id)
				}
			}
			// Keyset order is preserved inside a filtered page.
			for i := 1; i < len(got); i++ {
				prev, cur := got[i-1], got[i]
				if prev.CreatedAt < cur.CreatedAt {
					t.Fatalf("%s limit=%d: not descending by created_at at %d", category, limit, i)
				}
				if prev.CreatedAt == cur.CreatedAt && prev.ID <= cur.ID {
					t.Fatalf("%s limit=%d: ties not ordered by id DESC at %d", category, limit, i)
				}
			}
		}
	}
}

func TestCategoryFieldIsConsistentAcrossResponses(t *testing.T) {
	h := newHarness(t)
	h.pair()

	text := h.createText("text", "hello", "d")
	link := h.createText("link", "https://example.com", "d")
	zipBody := []byte("PK\x03\x04" + strings.Repeat("\x00", 40))
	res, body := h.upload("report.docx", zipBody, "d")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d body=%s", res.StatusCode, body)
	}
	doc := decodeItem(t, body)

	cases := []struct {
		item Item
		want string
	}{
		{text, "text"},
		{link, "link"},
		{doc, "document"},
	}
	for _, c := range cases {
		if string(c.item.Category) != c.want {
			t.Errorf("create response for %s category = %q, want %q", c.item.ID, c.item.Category, c.want)
		}
		res := h.do(http.MethodGet, "/api/v1/items/"+c.item.ID, nil)
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("get %s = %d body=%s", c.item.ID, res.StatusCode, raw)
		}
		got := decodeItem(t, raw)
		if string(got.Category) != c.want {
			t.Errorf("get %s category = %q, want %q", c.item.ID, got.Category, c.want)
		}
	}

	listed, _ := h.list("", 10)
	if len(listed) != 3 {
		t.Fatalf("listed %d items", len(listed))
	}
	byID := map[string]string{}
	for _, it := range listed {
		if it.Category == "" {
			t.Errorf("list item %s has empty category", it.ID)
		}
		byID[it.ID] = string(it.Category)
	}
	for _, c := range cases {
		if byID[c.item.ID] != c.want {
			t.Errorf("list %s category = %q, want %q", c.item.ID, byID[c.item.ID], c.want)
		}
	}
}
