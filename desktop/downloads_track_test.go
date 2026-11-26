package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseDownloadRequestAcceptsOnlyTheInboxItemRoute(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		ok   bool
		key  downloadKey
	}{
		{
			name: "plain",
			uri:  "http://127.0.0.1:8080/api/v1/items/abc123/download",
			ok:   true,
			key:  downloadKey{origin: "http://127.0.0.1:8080", id: "abc123"},
		},
		{
			name: "lan https origin",
			uri:  "https://hub.lan:9443/api/v1/items/x/download",
			ok:   true,
			key:  downloadKey{origin: "https://hub.lan:9443", id: "x"},
		},
		{
			// api.downloadUrl encodes the id with encodeURIComponent, so a "/" in
			// an id arrives as %2F and stays one path segment.
			name: "escaped id",
			uri:  "http://hub:8080/api/v1/items/a%2Fb/download",
			ok:   true,
			key:  downloadKey{origin: "http://hub:8080", id: "a/b"},
		},
		{
			name: "preview route is not a download",
			uri:  "http://hub:8080/api/v1/items/abc/preview",
			ok:   false,
		},
		{
			name: "other api path",
			uri:  "http://hub:8080/api/v1/session",
			ok:   false,
		},
		{
			name: "extra path segment",
			uri:  "http://hub:8080/api/v1/items/a/b/download",
			ok:   false,
		},
		{
			name: "empty id",
			uri:  "http://hub:8080/api/v1/items//download",
			ok:   false,
		},
		{
			name: "query is not the route the inbox uses",
			uri:  "http://hub:8080/api/v1/items/abc/download?x=1",
			ok:   false,
		},
		{
			name: "non http scheme",
			uri:  "file:///home/user/api/v1/items/abc/download",
			ok:   false,
		},
		{
			name: "relative",
			uri:  "/api/v1/items/abc/download",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, ok := parseDownloadRequest(tc.uri)
			if ok != tc.ok {
				t.Fatalf("triggered = %v, want %v", ok, tc.ok)
			}
			if ok && key != tc.key {
				t.Fatalf("key = %+v, want %+v", key, tc.key)
			}
		})
	}
}

func testTracker(files map[string]bool) *downloadTracker {
	tracker := newDownloadTracker()
	tracker.exists = func(path string) bool { return files[path] }
	tracker.setOrigin("http://hub:8080")
	return tracker
}

func TestDownloadTrackerDeduplicatesLocalRepeatAndAllowsRetryAfterDelete(t *testing.T) {
	key, ok := parseDownloadRequest("http://hub:8080/api/v1/items/item-1/download")
	if !ok {
		t.Fatal("parse failed")
	}
	saved := "/home/user/Downloads/report.pdf"
	files := map[string]bool{saved: true}
	tracker := testTracker(files)

	// First click: a real download, reported as downloading right away so the
	// page can stop waiting for a signal it will never get.
	if decision, ev := tracker.begin(key); decision != downloadTrackedNew || ev.Status != downloadStatusDownloading {
		t.Fatalf("first begin = %v %+v", decision, ev)
	}
	// Same item while it is still downloading: cancelled, no second file. A
	// pending record is never aged out, so a slow download cannot be duplicated.
	if decision, ev := tracker.begin(key); decision != downloadDuplicatePending || ev.Status != downloadStatusDownloading {
		t.Fatalf("repeat begin = %v %+v", decision, ev)
	}
	// Same item after a successful save while the file is still there.
	if ev, ok, deliver := tracker.finish(key, saved); !ok || !deliver || ev.Status != downloadStatusDone {
		t.Fatalf("finish = %+v %v %v", ev, ok, deliver)
	}
	if decision, ev := tracker.begin(key); decision != downloadDuplicateDone || !ev.Existing || ev.Status != downloadStatusDone {
		t.Fatalf("post-save begin = %v %+v", decision, ev)
	}
	// The user deleted the saved file: the next click must download again.
	delete(files, saved)
	if decision, ev := tracker.begin(key); decision != downloadTrackedNew || ev.Status != downloadStatusDownloading {
		t.Fatalf("begin after delete = %v %+v", decision, ev)
	}
}

func TestDownloadTrackerFailureIsRetryableAndReportsNoPath(t *testing.T) {
	key, _ := parseDownloadRequest("http://hub:8080/api/v1/items/item-2/download")
	tracker := testTracker(map[string]bool{})

	tracker.begin(key)
	ev, ok, deliver := tracker.fail(key)
	if !ok || !deliver || ev.ID != "item-2" || ev.Status != downloadStatusFailed {
		t.Fatalf("failure event = %+v %v %v", ev, ok, deliver)
	}
	// A failed attempt leaves no record, so the retry is a new download.
	if decision, _ := tracker.begin(key); decision != downloadTrackedNew {
		t.Fatalf("retry after failure = %v", decision)
	}

	// Whatever the tracker knows about the local path must never be in the
	// payload the page receives.
	if ev, ok, _ := tracker.finish(key, "/home/user/Downloads/secret-name.pdf"); ok {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "Downloads") || strings.Contains(string(raw), "secret-name") {
			t.Fatalf("event exposes a local path: %s", raw)
		}
	} else {
		t.Fatal("finish of a tracked download was ignored")
	}
}

func TestDownloadTrackerIsScopedToTheInboxHubOrigin(t *testing.T) {
	tracker := testTracker(map[string]bool{})

	foreign, _ := parseDownloadRequest("http://other-hub:8080/api/v1/items/item-1/download")
	if decision, _ := tracker.begin(foreign); decision != downloadUntracked {
		t.Fatalf("foreign origin = %v", decision)
	}
	own, _ := parseDownloadRequest("http://hub:8080/api/v1/items/item-1/download")
	if decision, _ := tracker.begin(own); decision != downloadTrackedNew {
		t.Fatalf("own origin = %v", decision)
	}
}

// TestDownloadTrackerKeepsRecordsAcrossHubSwitch is the A→B→A regression: a Hub
// switch must not make an already saved item download a second copy after the
// user returns, and a download started on the old Hub still updates its own
// record without reaching the new Hub's page.
func TestDownloadTrackerKeepsRecordsAcrossHubSwitch(t *testing.T) {
	const hubA = "http://hub-a:8080"
	const hubB = "https://hub-b:9443"
	savedFile := "/home/user/Downloads/a.pdf"
	lateFile := "/home/user/Downloads/late.pdf"
	files := map[string]bool{savedFile: true, lateFile: true}
	tracker := newDownloadTracker()
	tracker.exists = func(path string) bool { return files[path] }
	tracker.setOrigin(hubA)

	savedKey, _ := parseDownloadRequest(hubA + "/api/v1/items/saved-1/download")
	lateKey, _ := parseDownloadRequest(hubA + "/api/v1/items/late-1/download")
	failedKey, _ := parseDownloadRequest(hubA + "/api/v1/items/failed-1/download")
	if decision, _ := tracker.begin(savedKey); decision != downloadTrackedNew {
		t.Fatalf("begin saved = %v", decision)
	}
	if _, updated, deliver := tracker.finish(savedKey, savedFile); !updated || !deliver {
		t.Fatalf("finish saved = updated %v deliver %v", updated, deliver)
	}
	if decision, _ := tracker.begin(lateKey); decision != downloadTrackedNew {
		t.Fatalf("begin late = %v", decision)
	}
	tracker.setOrigin(hubB)
	// The new Hub's page cannot start a download for A's item route.
	if decision, _ := tracker.begin(savedKey); decision != downloadUntracked {
		t.Fatalf("A route while on B = %v", decision)
	}
	// A's in-flight download finishes while B is shown: its record is updated,
	// but nothing is delivered to B's page.
	if _, updated, deliver := tracker.finish(lateKey, lateFile); !updated || deliver {
		t.Fatalf("late finish = updated %v deliver %v", updated, deliver)
	}
	if decision, _ := tracker.begin(failedKey); decision != downloadUntracked {
		t.Fatalf("begin failed while on B = %v", decision)
	}
	// Back on A: both saved records still answer, so a repeat is cancelled
	// instead of saving a second copy.
	tracker.setOrigin(hubA)
	if decision, ev := tracker.begin(savedKey); decision != downloadDuplicateDone || !ev.Existing {
		t.Fatalf("A saved after return = %v %+v", decision, ev)
	}
	if decision, ev := tracker.begin(lateKey); decision != downloadDuplicateDone || !ev.Existing {
		t.Fatalf("A late finish after return = %v %+v", decision, ev)
	}
	// A failure of an old Hub download clears only its own record and is silent.
	if decision, _ := tracker.begin(failedKey); decision != downloadTrackedNew {
		t.Fatalf("begin failed back on A = %v", decision)
	}
	tracker.setOrigin(hubB)
	if _, updated, deliver := tracker.fail(failedKey); !updated || deliver {
		t.Fatalf("cross-origin fail = updated %v deliver %v", updated, deliver)
	}
	tracker.setOrigin(hubA)
	if decision, _ := tracker.begin(failedKey); decision != downloadTrackedNew {
		t.Fatalf("A retry after cross-origin failure = %v", decision)
	}
}

func TestDownloadTrackerIgnoresUntrackedOutcome(t *testing.T) {
	tracker := testTracker(map[string]bool{})
	key, _ := parseDownloadRequest("http://hub:8080/api/v1/items/item-4/download")

	// An outcome for something this process never began (e.g. a cancelled
	// duplicate, which has no handlers) must not invent a "done" status.
	if _, ok, _ := tracker.finish(key, "/home/user/Downloads/x"); ok {
		t.Fatal("untracked finish reported")
	}
	if _, ok, _ := tracker.fail(key); ok {
		t.Fatal("untracked failure reported")
	}
	if decision, _ := tracker.begin(key); decision != downloadTrackedNew {
		t.Fatalf("begin = %v", decision)
	}
}

func TestDownloadStatusBridgeIsOnlyInjectedWhereNativeReports(t *testing.T) {
	js := inboxInjectedJS("token")
	injected := strings.Contains(js, "flowdrop:download")
	if injected != downloadStatusBridgeEnabled {
		t.Fatalf("bridge injected = %v, want %v", injected, downloadStatusBridgeEnabled)
	}
	// One-way only: the status bridge must not hand the Hub page a native call.
	if strings.Contains(desktopDownloadStatusJS, "_wails") ||
		strings.Contains(desktopDownloadStatusJS, "invoke(") {
		t.Fatal("download status bridge exposes a native call to the page")
	}
	for _, want := range []string{
		"window.__flowdropDesktopDownloads = true",
		`new CustomEvent("flowdrop:download"`,
	} {
		if !strings.Contains(desktopDownloadStatusJS, want) {
			t.Fatalf("bridge is missing %q", want)
		}
	}
}
