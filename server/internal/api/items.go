package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"flowdrop/internal/events"
	"flowdrop/internal/store"
)

// Version is set at build time via -ldflags.
var Version = "dev"

const (
	defaultPageLimit = 30
	maxPageLimit     = 100
	maxSenderLen     = 64
	maxNameLen       = 200
)

type createTextRequest struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	SenderDevice string `json:"sender_device"`
}

func (s *Server) handleCreateText(w http.ResponseWriter, r *http.Request) {
	var req createTextRequest
	if err := s.readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	text := strings.TrimRight(req.Text, "\n")
	if strings.TrimSpace(text) == "" {
		writeError(w, http.StatusBadRequest, "empty_text", "text must not be empty")
		return
	}
	if len(text) > maxTextBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "text_too_large",
			fmt.Sprintf("text exceeds %d bytes", maxTextBytes))
		return
	}
	if !utf8.ValidString(text) {
		writeError(w, http.StatusBadRequest, "invalid_text", "text must be valid UTF-8")
		return
	}

	itemType := strings.ToLower(strings.TrimSpace(req.Type))
	if itemType == "" {
		itemType = "text"
	}
	switch itemType {
	case "text":
	case "link":
		if !validHTTPURL(text) {
			writeError(w, http.StatusBadRequest, "invalid_link", "link must be an absolute http(s) URL")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid_type", "type must be 'text' or 'link'")
		return
	}

	sender := sanitizeSender(req.SenderDevice)
	it, err := s.store.CreateTextItem(store.NewID(), itemType, text, sender)
	if err != nil {
		s.log.Error("create text item", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not save item")
		return
	}
	s.broker.Publish(events.Event{Type: "item.created", ID: it.ID})
	writeJSON(w, http.StatusCreated, it)
}

func validHTTPURL(s string) bool {
	if len(s) > 4096 || strings.ContainsAny(s, "\r\n\t ") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func sanitizeSender(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if utf8.RuneCountInString(s) > maxSenderLen {
		s = string([]rune(s)[:maxSenderLen])
	}
	return s
}

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	limit := defaultPageLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageLimit {
			writeError(w, http.StatusBadRequest, "bad_request",
				fmt.Sprintf("limit must be an integer between 1 and %d", maxPageLimit))
			return
		}
		limit = n
	}
	var cursor *store.Cursor
	if v := r.URL.Query().Get("cursor"); v != "" {
		c, err := store.DecodeCursor(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_cursor", "invalid pagination cursor")
			return
		}
		cursor = &c
	}
	category := store.CategoryAll
	if v := r.URL.Query().Get("category"); v != "" {
		c, ok := store.ParseCategory(v)
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request",
				"category must be one of: "+categoryList())
			return
		}
		category = c
	}
	items, next, err := s.store.ListItemsByCategory(cursor, category, limit)
	if err != nil {
		s.log.Error("list items", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not list items")
		return
	}
	nextCursor := ""
	if next != nil {
		nextCursor = store.EncodeCursor(store.Item{CreatedAt: next.CreatedAt, ID: next.ID})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
	})
}

// categoryList renders the accepted category values for error messages.
func categoryList() string {
	names := make([]string, 0, len(store.Categories()))
	for _, c := range store.Categories() {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	it, err := s.store.GetItem(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	if err != nil {
		s.log.Error("get item", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not load item")
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	blobKey, err := s.store.DeleteItem(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	if err != nil {
		s.log.Error("delete item", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not delete item")
		return
	}
	s.broker.Publish(events.Event{Type: "item.deleted", ID: id})
	if blobKey != "" {
		if err := s.files.RemoveBlob(blobKey); err != nil {
			// Row is already gone; the janitor retries unreferenced files.
			s.log.Warn("remove blob file", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadResponse is the created item; the SPA inserts it at the top.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	select {
	case s.uploadSem <- struct{}{}:
		defer func() { <-s.uploadSem }()
	default:
		writeError(w, http.StatusTooManyRequests, "too_many_uploads",
			"too many concurrent uploads, retry shortly")
		return
	}

	// Bound how long one upload may hold a concurrency slot, so a stalled client
	// cannot starve the upload semaphore.
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(s.uploadReadTimeout())); err == nil {
		defer func() { _ = rc.SetReadDeadline(time.Time{}) }()
	}

	if avail, err := s.files.AvailableBytes(); err == nil {
		if avail < uint64(s.cfg.MinFreeBytes)+uint64(s.cfg.MaxUploadBytes)/8 {
			writeError(w, http.StatusInsufficientStorage, "disk_full",
				"not enough free disk space on the Hub")
			return
		}
	}

	// Allow multipart framing overhead on top of the file limit.
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes+1<<20)

	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_multipart", "expected multipart/form-data upload")
		return
	}

	var (
		sender      string
		tempFile    *filePart
		fileName    string
		uploadedErr error
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeUploadError(w, err, s.cfg.MaxUploadBytes)
			return
		}
		switch part.FormName() {
		case "sender_device":
			b, _ := io.ReadAll(io.LimitReader(part, 1024))
			sender = sanitizeSender(string(b))
			part.Close()
		case "file":
			if tempFile != nil {
				part.Close()
				s.files.RemoveTemp(tempFile.path)
				writeError(w, http.StatusBadRequest, "too_many_files",
					"send one file per request")
				return
			}
			fileName = part.FileName()
			tempFile, uploadedErr = s.streamToTemp(part)
			part.Close()
			if uploadedErr != nil {
				writeUploadError(w, uploadedErr, s.cfg.MaxUploadBytes)
				return
			}
		default:
			part.Close()
		}
	}
	if tempFile == nil {
		writeError(w, http.StatusBadRequest, "no_file", "no 'file' part in upload")
		return
	}
	if tempFile.size == 0 && fileName == "" {
		s.files.RemoveTemp(tempFile.path)
		writeError(w, http.StatusBadRequest, "no_file", "empty upload without a filename")
		return
	}

	name := sanitizeFileName(fileName)
	ctype, itemType := classifyUpload(name, tempFile.head)
	blobKey, err := s.files.NewBlobKey()
	if err != nil {
		s.files.RemoveTemp(tempFile.path)
		writeError(w, http.StatusInternalServerError, "internal", "could not allocate storage")
		return
	}
	if err := s.files.CommitTemp(tempFile.path, blobKey); err != nil {
		s.files.RemoveTemp(tempFile.path)
		s.log.Error("commit blob", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not store file")
		return
	}

	blob := store.Blob{
		ID:           store.NewID(),
		OriginalName: name,
		ContentType:  ctype,
		SizeBytes:    tempFile.size,
		SHA256:       tempFile.sha256,
		StorageKey:   blobKey,
	}
	it, err := s.store.CreateBlobItem(blob, itemType, sender)
	if err != nil {
		// Compensate: drop the file we just moved; janitor also covers this.
		_ = s.files.RemoveBlob(blobKey)
		s.log.Error("create blob item", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not save item")
		return
	}
	s.broker.Publish(events.Event{Type: "item.created", ID: it.ID})
	writeJSON(w, http.StatusCreated, it)
}

func (s *Server) uploadReadTimeout() time.Duration {
	if s.uploadTimeout <= 0 {
		return 15 * time.Minute
	}
	return s.uploadTimeout
}

type filePart struct {
	path   string
	size   int64
	sha256 string
	head   []byte
}

// streamToTemp writes the part to a temp file while hashing, enforcing the
// configured size limit. On any error the temp file is removed.
func (s *Server) streamToTemp(r io.Reader) (*filePart, error) {
	f, path, err := s.files.NewTempFile()
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	head := make([]byte, 512)
	headN := 0
	limited := io.LimitReader(r, s.cfg.MaxUploadBytes+1)
	buf := make([]byte, 128<<10)
	var size int64
	for {
		n, rerr := limited.Read(buf)
		if n > 0 {
			if headN < len(head) {
				copy(head[headN:], buf[:n])
				headN += min(n, len(head)-headN)
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				s.files.RemoveTemp(path)
				return nil, werr
			}
			hash.Write(buf[:n])
			size += int64(n)
			if size > s.cfg.MaxUploadBytes {
				f.Close()
				s.files.RemoveTemp(path)
				return nil, errTooLarge
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			s.files.RemoveTemp(path)
			return nil, rerr
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		s.files.RemoveTemp(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		s.files.RemoveTemp(path)
		return nil, err
	}
	return &filePart{
		path:   path,
		size:   size,
		sha256: hex.EncodeToString(hash.Sum(nil)),
		head:   head[:headN],
	}, nil
}

var errTooLarge = errors.New("upload exceeds size limit")

func writeUploadError(w http.ResponseWriter, err error, limit int64) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, errTooLarge), errors.As(err, &maxErr):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("file exceeds the %s limit", humanBytes(limit)))
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		writeError(w, http.StatusBadRequest, "incomplete_upload", "upload was interrupted")
	default:
		writeError(w, http.StatusBadRequest, "upload_failed", "upload failed: "+err.Error())
	}
}

// sanitizeFileName strips directory components, control characters and
// overlong names. It is only ever used for display / Content-Disposition.
func sanitizeFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	name = strings.TrimLeft(name, ".")
	if name == "" {
		name = "file"
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		ext := filepath.Ext(name)
		if len([]rune(ext)) > 16 {
			ext = ""
		}
		runes := []rune(name)
		name = string(runes[:maxNameLen-len([]rune(ext))]) + ext
	}
	return name
}

// activeContentExt reports whether the file type can execute script in a
// browser context. Such files are always served as attachments.
func activeContentExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".svg", ".svgz", ".html", ".htm", ".xhtml", ".xml", ".js", ".mjs", ".css", ".json":
		return true
	}
	return false
}

// classifyUpload decides the stored content type and inbox item type. Never
// trust the client-declared MIME type: the bytes are sniffed, and the extension
// only ever narrows what is allowed, never widens it.
func classifyUpload(name string, head []byte) (contentType, itemType string) {
	detected := "application/octet-stream"
	if len(head) > 0 {
		detected = strings.ToLower(strings.SplitN(http.DetectContentType(head), ";", 2)[0])
	}
	ext := strings.ToLower(filepath.Ext(name))

	// Active content is always an opaque download, whatever the bytes look like.
	if activeContentExt(name) {
		return "application/octet-stream", "file"
	}

	// Only a sniffed, raster-type image may be previewed inline.
	switch detected {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return detected, "image"
	}

	// Non-previewable file. Keep a useful, sniffed type unless the sniff says
	// the payload is markup/script (then it is forced opaque so no browser can
	// be tricked into rendering it even if a client ignores the attachment
	// disposition).
	switch detected {
	case "text/html", "text/xml", "image/svg+xml", "application/xhtml+xml",
		"application/javascript", "text/javascript", "application/json":
		return "application/octet-stream", "file"
	}
	if detected != "application/octet-stream" {
		return detected, "file"
	}
	if mt := mime.TypeByExtension(ext); mt != "" {
		mt = strings.ToLower(strings.SplitN(mt, ";", 2)[0])
		// The extension must not introduce active content either.
		if strings.Contains(mt, "html") || strings.Contains(mt, "svg") ||
			strings.Contains(mt, "xml") || strings.Contains(mt, "javascript") {
			return "application/octet-stream", "file"
		}
		return mt, "file"
	}
	return "application/octet-stream", "file"
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	s.serveBlob(w, r, false)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	s.serveBlob(w, r, true)
}

// serveBlob streams an attachment with auth already enforced. inline is only
// honored for safe raster image types.
func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request, inline bool) {
	it, err := s.store.GetItem(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	if err != nil {
		s.log.Error("download lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not load item")
		return
	}
	if it.Blob == nil {
		writeError(w, http.StatusNotFound, "no_attachment", "this item has no attachment")
		return
	}
	f, err := s.files.OpenBlob(it.Blob.StorageKey)
	if err != nil {
		s.log.Error("open blob", "err", err)
		writeError(w, http.StatusNotFound, "not_found", "attachment file is missing")
		return
	}
	defer f.Close()

	h := w.Header()
	h.Set("ETag", `"sha256-`+it.Blob.SHA256+`"`)
	h.Set("X-Content-SHA256", it.Blob.SHA256)
	h.Set("Content-Disposition", contentDisposition(
		it.Blob.OriginalName, inline && it.Blob.IsPreviewableImage()))
	if inline && it.Blob.IsPreviewableImage() {
		h.Set("Content-Type", it.Blob.ContentType)
	} else {
		h.Set("Content-Type", "application/octet-stream")
	}
	h.Set("Cache-Control", "private, max-age=0, must-revalidate")

	http.ServeContent(w, r, "", time.UnixMilli(it.Blob.CreatedAt), f)
}

func contentDisposition(name string, inline bool) string {
	kind := "attachment"
	if inline {
		kind = "inline"
	}
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if ascii == "" {
		ascii = "file"
	}
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`,
		kind, ascii, url.PathEscape(name))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
