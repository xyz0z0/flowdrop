package main

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"sync"
)

// Why this file exists: WebKitGTK reports a download twice — "failed" is emitted
// only when something went wrong, "finished" is emitted afterwards in both cases
// (WebKit2-4.1.gir, WebKitDownload::failed / ::finished, Source/WebKit/UIProcess/
// API/glib/WebKitDownload.cpp). "finished without failed" is therefore the only
// real success signal the native side has.
//
// The desktop needs that signal to survive the page: a reload, a second window
// state or a repeat click must not write the same Hub item twice, and a download
// that failed must be retryable. The tracker below is the process-wide authority
// for both. It is keyed by the Hub origin plus the item id taken from the exact
// download route, so one Hub's item can never deduplicate another Hub's, and the
// page is only ever told the item id and a status (never a local path).
//
// Everything here is pure Go so the dedupe/retry rules are testable with
// -tags purego on any platform; the WebKit signal wiring lives in
// downloads_linux_gtk3.go.

const (
	// downloadRoutePrefix and downloadRouteSuffix bracket the only Hub route the
	// inbox turns into a tracked download (server/internal/api/server.go,
	// "GET /api/v1/items/{id}/download", built by web/src/api.ts).
	downloadRoutePrefix = "/api/v1/items/"
	downloadRouteSuffix = "/download"
)

// downloadStatus is the whole vocabulary the page receives.
type downloadStatus string

const (
	downloadStatusDownloading downloadStatus = "downloading"
	downloadStatusDone        downloadStatus = "done"
	downloadStatusFailed      downloadStatus = "failed"
)

// downloadEvent is one report to the page. It carries no local path: the user's
// download directory never reaches the Web layer.
type downloadEvent struct {
	ID     string         `json:"id"`
	Status downloadStatus `json:"status"`
	// Existing marks a repeat cancelled because the same item is already saved:
	// the page answers "已下载，不重复保存" instead of "下载已完成".
	Existing bool `json:"existing,omitempty"`
}

// downloadKey identifies one Hub item download. origin is scheme://host[:port],
// id is the decoded {id} path segment of the download route.
type downloadKey struct {
	origin string
	id     string
}

// downloadDecision is what the WebKit "download-started" handler must do.
type downloadDecision int

const (
	// downloadUntracked: not this inbox's item route (another origin, another
	// backend, a link the page does not own). WebKit keeps its normal behaviour.
	downloadUntracked downloadDecision = iota
	// downloadTrackedNew: a real new attempt; the download proceeds and its
	// outcome is reported.
	downloadTrackedNew
	// downloadDuplicatePending: the same item is downloading already; cancel the
	// new download and report "downloading" instead of writing a second file.
	downloadDuplicatePending
	// downloadDuplicateDone: the same item is already saved; cancel and report.
	downloadDuplicateDone
)

// parseDownloadRequest turns a WebKit request URI into a tracked key. Only an
// absolute http(s) URL on the exact item download route qualifies: no query, no
// fragment, no extra path segments. Anything else is left to WebKit, so a plain
// browser navigation or another Hub page can never be deduplicated by accident.
func parseDownloadRequest(raw string) (downloadKey, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return downloadKey{}, false
	}
	// The inbox always requests the bare route. A query or fragment would be a
	// different URL the Hub never hands out for an item.
	if u.RawQuery != "" || u.Fragment != "" {
		return downloadKey{}, false
	}
	// EscapedPath keeps percent-encoded ids exactly as requested (the page builds
	// them with encodeURIComponent), so "/" inside an id stays inside the segment.
	path := u.EscapedPath()
	if !strings.HasPrefix(path, downloadRoutePrefix) || !strings.HasSuffix(path, downloadRouteSuffix) {
		return downloadKey{}, false
	}
	segment := strings.TrimSuffix(strings.TrimPrefix(path, downloadRoutePrefix), downloadRouteSuffix)
	if segment == "" || strings.Contains(segment, "/") {
		return downloadKey{}, false
	}
	id, err := url.PathUnescape(segment)
	if err != nil || id == "" {
		return downloadKey{}, false
	}
	return downloadKey{origin: u.Scheme + "://" + u.Host, id: id}, true
}

// downloadRecord is one tracked attempt. status is pending while WebKit writes
// and done after "finished without failed"; a failed attempt leaves no record so
// the next click is a new download.
type downloadRecord struct {
	status downloadStatus
	dest   string // native-only save path, used to notice a deleted file
}

// downloadTracker holds the native truth about this process's item downloads.
// The zero value is not usable; newDownloadTracker wires the real filesystem so
// tests can inject one.
type downloadTracker struct {
	mu      sync.Mutex
	origin  string // the Hub origin the inbox window is showing right now
	entries map[downloadKey]*downloadRecord
	exists  func(string) bool
}

func newDownloadTracker() *downloadTracker {
	return &downloadTracker{
		entries: make(map[downloadKey]*downloadRecord),
		exists:  downloadPathExists,
	}
}

// downloadPathExists follows the same rule as uniqueDownloadPath: a path that
// cannot be inspected for any reason other than "not there" counts as taken, so
// a permission problem never turns a repeat click into an overwrite.
func downloadPathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// setOrigin records the Hub origin the inbox window currently shows. Records of
// other origins are kept on purpose: a download started just before a Hub switch
// may still be writing, and switching back must not save the same item a second
// time. Delivery is gated separately (see finish/fail and sendDownloadEvent), so
// updating a record of another origin never reaches the current page.
func (t *downloadTracker) setOrigin(origin string) {
	t.mu.Lock()
	t.origin = origin
	t.mu.Unlock()
}

// begin decides what a newly started WebKit download should do and which status
// (if any) the page gets. A pending record is only ever cleared by its own
// "failed" or "finished" signal, so a repeat click during a genuinely long
// download can never become a second file.
func (t *downloadTracker) begin(key downloadKey) (downloadDecision, downloadEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.origin == "" || key.origin != t.origin {
		return downloadUntracked, downloadEvent{}
	}
	if rec, ok := t.entries[key]; ok {
		switch rec.status {
		case downloadStatusDownloading:
			return downloadDuplicatePending, downloadEvent{ID: key.id, Status: downloadStatusDownloading}
		case downloadStatusDone:
			// An empty destination means the save path is unknown, which is
			// treated like "still there": never write a second copy unseen.
			if rec.dest == "" || t.exists(rec.dest) {
				return downloadDuplicateDone, downloadEvent{ID: key.id, Status: downloadStatusDone, Existing: true}
			}
			// The saved file was deleted, so this repeat is a real download again.
		}
	}
	t.entries[key] = &downloadRecord{status: downloadStatusDownloading}
	return downloadTrackedNew, downloadEvent{ID: key.id, Status: downloadStatusDownloading}
}

// finish records a successful WebKit download ("finished" without "failed").
// destination stays native-only; only the id and status reach the page. The
// per-download failure marker in downloads_linux_gtk3.go guarantees that only the
// attempt that really finished can reach this, never a stale callback.
func (t *downloadTracker) finish(key downloadKey, destination string) (downloadEvent, bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.entries[key]
	if !ok || rec.status != downloadStatusDownloading {
		return downloadEvent{}, false, false
	}
	rec.status = downloadStatusDone
	rec.dest = destination
	// updated, and deliver only while this key belongs to the Hub the inbox
	// currently shows (the caller also checks the initiating view's origin).
	return downloadEvent{ID: key.id, Status: downloadStatusDone}, true, t.origin != "" && key.origin == t.origin
}

// fail drops the record so the item is immediately retryable and tells the page
// that this attempt did not land. A download of another Hub origin still clears
// its own record, it is only not delivered to the current page.
func (t *downloadTracker) fail(key downloadKey) (downloadEvent, bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rec, ok := t.entries[key]
	if !ok || rec.status != downloadStatusDownloading {
		return downloadEvent{}, false, false
	}
	delete(t.entries, key)
	return downloadEvent{ID: key.id, Status: downloadStatusFailed}, true, t.origin != "" && key.origin == t.origin
}

// processDownloads is the tracker for this process. The desktop has exactly one
// inbox window and one Hub origin at a time, so a package-level tracker is the
// same single authority the WebKit context callbacks already imply.
var processDownloads = newDownloadTracker()

// setDownloadInboxOrigin publishes the Hub origin the inbox window shows. It is
// called from openInbox, i.e. on every load, reload and Hub switch.
func setDownloadInboxOrigin(origin string) {
	processDownloads.setOrigin(origin)
}

// desktopDownloadStatusJS is the page half of the native download status. It is
// injected only into the Hub-origin inbox window of a Linux GTK3 build
// (downloadStatusBridgeEnabled) and is intentionally one-way: the native side
// calls window.__flowdropDownload with {id, status, existing?}, and the page
// listens for a CustomEvent. The Hub page can call no native method through it,
// and no local save path is ever part of the payload.
const desktopDownloadStatusJS = `(function () {
  if (window.__flowdropDesktopDownloads) { return; }
  window.__flowdropDesktopDownloads = true;
  window.__flowdropDownload = function (event) {
    try {
      if (!event || typeof event.id !== "string" || typeof event.status !== "string") { return; }
      window.dispatchEvent(new CustomEvent("flowdrop:download", {
        detail: { id: event.id, status: event.status, existing: event.existing === true }
      }));
    } catch (e) {}
  };
})();`
