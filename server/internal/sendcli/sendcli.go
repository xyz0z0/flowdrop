// Package sendcli is the CLI-side HTTP client for a FlowDrop Hub.
//
// It speaks the same frozen HTTP contract the SPA and the desktop use:
//
//   - POST /api/v1/auth/pair registers this machine with a locally shown
//     4-digit code and returns a device token exactly once.
//   - GET /api/v1/auth/session restores the CSRF cookies for a Bearer device
//     token and reports the Hub's upload limit.
//   - POST /api/v1/items/upload and POST /api/v1/items/text create items.
//
// Every state-changing request needs the double-submit CSRF token, so a Client
// must be Connected (cookie jar primed, CSRF header value known) before it can
// send anything. There is deliberately no automatic retry: pairing codes are
// single-use and uploads are not idempotent.
package sendcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	csrfCookieName = "flowdrop_csrf"
	csrfHeaderName = "X-FlowDrop-CSRF"

	// maxErrorBodyBytes bounds how much of an error response is read.
	maxErrorBodyBytes = 64 << 10
	// maxJSONBodyBytes bounds successful JSON responses.
	maxJSONBodyBytes = 1 << 20
)

// Credentials is the saved CLI login. DeviceToken is a secret: it is only ever
// stored in the user's config file and never echoed in errors.
type Credentials struct {
	HubURL      string `json:"hub_url"`
	DeviceToken string `json:"device_token"`
	DeviceID    string `json:"device_id"`
	DeviceName  string `json:"device_name"`
	CreatedAt   int64  `json:"created_at"`
}

var (
	ErrNoCredentials = errors.New("no saved flowdrop CLI credentials")
	ErrUnauthorized  = errors.New("flowdrop device token rejected")
)

// ErrNotConnected is returned by the send methods before a successful Connect.
// It is unexported on purpose: callers only need Connect to succeed.
var errNotConnected = errors.New("flowdrop: client is not connected; call Connect first")

// Error is an error response from the Hub.
type Error struct {
	Status  int
	Code    string
	Message string
}

// Error renders a single line such as "flowdrop: 413 too_large: file exceeds
// the 1.0 MiB limit". Neither the device token nor the pairing code is ever
// included.
func (e *Error) Error() string {
	if e == nil {
		return "flowdrop: <nil>"
	}
	if e.Code != "" {
		return fmt.Sprintf("flowdrop: %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("flowdrop: %d: %s", e.Status, e.Message)
}

// Item is a Hub inbox item as returned by upload/text endpoints.
type Item struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	CreatedAt int64  `json:"created_at"`
	Blob      *Blob  `json:"blob,omitempty"`
}

// Blob describes the stored file of a file item. The Hub serializes the
// original file name as "name" (store.Blob).
type Blob struct {
	OriginalName string `json:"name"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	ContentType  string `json:"content_type"`
}

// DefaultCredentialsPath returns $FLOWDROP_CLI_CONFIG when set, otherwise
// <os.UserConfigDir()>/flowdrop/cli.json.
func DefaultCredentialsPath() string {
	if p := strings.TrimSpace(os.Getenv("FLOWDROP_CLI_CONFIG")); p != "" {
		return p
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "flowdrop", "cli.json")
	}
	return filepath.Join("flowdrop", "cli.json")
}

// LoadCredentials reads the saved login. A missing file, and a file without a
// hub URL or device token, both report ErrNoCredentials.
func LoadCredentials(path string) (Credentials, error) {
	if strings.TrimSpace(path) == "" {
		return Credentials{}, ErrNoCredentials
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credentials{}, ErrNoCredentials
		}
		return Credentials{}, fmt.Errorf("flowdrop: read credentials %s: %w", path, err)
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return Credentials{}, fmt.Errorf("flowdrop: parse credentials %s: %w", path, err)
	}
	c.HubURL = strings.TrimSpace(c.HubURL)
	c.DeviceToken = strings.TrimSpace(c.DeviceToken)
	if c.HubURL == "" || c.DeviceToken == "" {
		return Credentials{}, ErrNoCredentials
	}
	return c, nil
}

// SaveCredentials writes the login atomically: a 0600 temp file in the target
// directory, then rename. The directory is created 0700.
func SaveCredentials(path string, c Credentials) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("flowdrop: credentials path must not be empty")
	}
	c.HubURL = strings.TrimSpace(c.HubURL)
	c.DeviceToken = strings.TrimSpace(c.DeviceToken)
	if c.HubURL == "" || c.DeviceToken == "" {
		return errors.New("flowdrop: refusing to save credentials without hub_url and device_token")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("flowdrop: create config directory %s: %w", dir, err)
	}
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("flowdrop: encode credentials: %w", err)
	}
	body = append(body, '\n')

	tmp, err := os.CreateTemp(dir, ".cli-*.json")
	if err != nil {
		return fmt.Errorf("flowdrop: create temp credentials in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := tmp.Write(body); err != nil {
		cleanup()
		return fmt.Errorf("flowdrop: write credentials: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("flowdrop: set credentials mode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("flowdrop: sync credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("flowdrop: close credentials: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("flowdrop: replace credentials %s: %w", path, err)
	}
	return nil
}

// normalizeHubURL validates an http(s) hub URL and reduces it to
// scheme://host[:port][/base-path] with no trailing slash, query or fragment.
func normalizeHubURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("flowdrop: hub URL must not be empty")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("flowdrop: invalid hub URL: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	switch u.Scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("flowdrop: hub URL must use http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("flowdrop: hub URL must include a host")
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

type sessionLimits struct {
	MaxUploadBytes int64 `json:"max_upload_bytes"`
}

// sessionInfo is the pairing/session response of the Hub.
type sessionInfo struct {
	Authenticated bool           `json:"authenticated"`
	CSRFToken     string         `json:"csrf_token"`
	ExpiresAt     int64          `json:"expires_at"`
	Limits        *sessionLimits `json:"limits"`
	DeviceID      string         `json:"device_id"`
	DeviceToken   string         `json:"device_token"`
}

type pairRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
	Native     bool   `json:"native"`
}

// Pair redeems a pairing code shown on the Hub and returns the new credentials
// without writing them to disk. No Origin header is sent: this is a native
// client, and the Hub allows origin-less requests while browsers cannot omit
// Origin.
func Pair(ctx context.Context, hubURL, code, deviceName string) (Credentials, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return Credentials{}, err
	}
	if strings.TrimSpace(code) == "" {
		return Credentials{}, errors.New("flowdrop: pairing code must not be empty")
	}
	name := strings.TrimSpace(deviceName)
	if name == "" {
		// Checked locally so an empty name never consumes a pairing attempt.
		return Credentials{}, errors.New("flowdrop: device name must not be empty")
	}

	body, err := json.Marshal(pairRequest{Code: strings.TrimSpace(code), DeviceName: name, Native: true})
	if err != nil {
		return Credentials{}, fmt.Errorf("flowdrop: encode pairing request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/pair", bytes.NewReader(body))
	if err != nil {
		return Credentials{}, fmt.Errorf("flowdrop: build pairing request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Pairing does not need cookies: the device token comes back in the body.
	// The client is short-lived so no cookie or connection state leaks between
	// login attempts.
	hc := &http.Client{}
	resp, err := hc.Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("flowdrop: pair with %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Credentials{}, responseError(resp)
	}

	var info sessionInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBodyBytes)).Decode(&info); err != nil {
		return Credentials{}, fmt.Errorf("flowdrop: decode pairing response: %w", err)
	}
	if !info.Authenticated || info.DeviceToken == "" || info.DeviceID == "" {
		return Credentials{}, errors.New("flowdrop: hub did not return a device token")
	}
	return Credentials{
		HubURL:      base,
		DeviceToken: info.DeviceToken,
		DeviceID:    info.DeviceID,
		DeviceName:  name,
		CreatedAt:   time.Now().UnixMilli(),
	}, nil
}

// Client sends items to one Hub as one paired device.
type Client struct {
	baseURL string
	token   string
	http    *http.Client

	mu             sync.Mutex
	connected      bool
	csrf           string
	maxUploadBytes int64
	// senderDevice is the display label sent as sender_device. Empty means
	// the field is omitted.
	senderDevice string
}

// NewClient validates the hub URL and returns a client with a cookie jar. The
// jar is required: the Hub reissues the session and CSRF cookies when the
// client authenticates with a Bearer device token.
func NewClient(hubURL, token string) (*Client, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("flowdrop: create cookie jar: %w", err)
	}
	return &Client{
		baseURL: base,
		token:   strings.TrimSpace(token),
		// No client timeout: every call is bounded by the caller's context so a
		// long upload is not cut off by an arbitrary budget.
		http: &http.Client{Jar: jar},
	}, nil
}

// Connect exchanges the device token for the CSRF value and upload limit.
func (c *Client) Connect(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/v1/auth/session", nil)
	if err != nil {
		return err
	}
	resp, err := c.roundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var info sessionInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBodyBytes)).Decode(&info); err != nil {
		return fmt.Errorf("flowdrop: decode session response: %w", err)
	}
	if !info.Authenticated {
		// The Hub answers 200 with authenticated=false for an unknown or
		// revoked token; that is exactly ErrUnauthorized.
		return ErrUnauthorized
	}
	csrf := info.CSRFToken
	if csrf == "" {
		csrf = c.cookieValue(csrfCookieName)
	}
	if csrf == "" {
		return errors.New("flowdrop: hub did not issue a CSRF token")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.csrf = csrf
	c.maxUploadBytes = 0
	if info.Limits != nil {
		c.maxUploadBytes = info.Limits.MaxUploadBytes
	}
	c.connected = true
	return nil
}

// SetSenderDevice sets the client-supplied display label sent as
// sender_device with every item (normally the paired device name). The value is
// trimmed; an empty string means the field is not sent at all. The Hub still
// sanitizes and truncates it, so the CLI does not truncate here.
func (c *Client) SetSenderDevice(name string) {
	name = strings.TrimSpace(name)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.senderDevice = name
}

// senderName returns the current display label, or "" when unset.
func (c *Client) senderName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.senderDevice
}

// MaxUploadBytes reports the Hub's per-file limit, or 0 when unknown (before a
// successful Connect). Callers skip their own pre-check when it is 0.
func (c *Client) MaxUploadBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxUploadBytes
}

// SendFile uploads a regular file. The body is streamed from disk; the file is
// never read into memory.
func (c *Client) SendFile(ctx context.Context, path string) (Item, error) {
	if _, err := c.csrfToken(); err != nil {
		return Item{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return Item{}, fmt.Errorf("flowdrop: open %s: %w", path, err)
	}
	// A directory or device is a caller mistake, not an empty upload.
	if !st.Mode().IsRegular() {
		return Item{}, fmt.Errorf("flowdrop: %s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return Item{}, fmt.Errorf("flowdrop: open %s: %w", path, err)
	}
	defer f.Close()

	item, err := c.SendReader(ctx, filepath.Base(path), st.Size(), f)
	if err != nil {
		return Item{}, err
	}
	return item, nil
}

// SendReader uploads size bytes read from r as name. The caller has already
// spooled the data (for example to a temp file). A negative size, or a reader
// that does not yield exactly size bytes, is an error: the payload is never
// silently truncated.
func (c *Client) SendReader(ctx context.Context, name string, size int64, r io.Reader) (Item, error) {
	csrf, err := c.csrfToken()
	if err != nil {
		return Item{}, err
	}
	if size < 0 {
		return Item{}, fmt.Errorf("flowdrop: upload size must not be negative (got %d)", size)
	}
	if strings.TrimSpace(name) == "" {
		return Item{}, errors.New("flowdrop: upload file name must not be empty")
	}
	if r == nil {
		return Item{}, errors.New("flowdrop: upload reader must not be nil")
	}

	// Build the multipart prefix and suffix once, then stream the payload
	// between them. The exact Content-Length is the sum of all three, so the
	// Hub never has to accept a chunked body and no file-sized buffer is
	// allocated. The optional sender_device field goes into the prefix, before
	// the file part, so the file bytes stay streamable.
	var prefix bytes.Buffer
	mw := multipart.NewWriter(&prefix)
	if sender := c.senderName(); sender != "" {
		if err := mw.WriteField("sender_device", sender); err != nil {
			return Item{}, fmt.Errorf("flowdrop: build multipart body: %w", err)
		}
	}
	if _, err := mw.CreateFormFile("file", name); err != nil {
		return Item{}, fmt.Errorf("flowdrop: build multipart body: %w", err)
	}
	headLen := prefix.Len()
	contentType := mw.FormDataContentType()
	if err := mw.Close(); err != nil {
		return Item{}, fmt.Errorf("flowdrop: build multipart body: %w", err)
	}
	head := prefix.Bytes()[:headLen]
	tail := prefix.Bytes()[headLen:]

	counted := &countingReader{r: r}
	body := io.MultiReader(bytes.NewReader(head), counted, bytes.NewReader(tail))

	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/items/upload", body)
	if err != nil {
		return Item{}, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set(csrfHeaderName, csrf)
	// Set the exact length: the file size plus the multipart framing.
	req.ContentLength = int64(len(head)) + size + int64(len(tail))

	resp, err := c.roundTrip(req)
	if err != nil {
		return Item{}, err
	}
	defer resp.Body.Close()
	if counted.n != size {
		return Item{}, fmt.Errorf("flowdrop: upload body had %d bytes, declared %d", counted.n, size)
	}
	return decodeItem(resp)
}

// SendText creates a text or link item. kind is "text" or "link".
func (c *Client) SendText(ctx context.Context, kind, text string) (Item, error) {
	switch kind {
	case "text", "link":
	default:
		return Item{}, fmt.Errorf("flowdrop: kind must be \"text\" or \"link\", got %q", kind)
	}
	csrf, err := c.csrfToken()
	if err != nil {
		return Item{}, err
	}
	body, err := json.Marshal(struct {
		Type         string `json:"type"`
		Text         string `json:"text"`
		SenderDevice string `json:"sender_device,omitempty"`
	}{Type: kind, Text: text, SenderDevice: c.senderName()})
	if err != nil {
		return Item{}, fmt.Errorf("flowdrop: encode text item: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/items/text", bytes.NewReader(body))
	if err != nil {
		return Item{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeaderName, csrf)

	resp, err := c.roundTrip(req)
	if err != nil {
		return Item{}, err
	}
	defer resp.Body.Close()
	return decodeItem(resp)
}

// csrfToken returns the CSRF value learned by Connect, or errNotConnected.
func (c *Client) csrfToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || c.csrf == "" {
		return "", errNotConnected
	}
	return c.csrf, nil
}

// newRequest builds an authenticated JSON-accepting request. It never sets an
// Origin header, so the Hub treats it as a native client.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("flowdrop: build %s %s request: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	return req, nil
}

// roundTrip performs the request and turns non-2xx responses into errors. The
// caller owns closing the returned response body.
func (c *Client) roundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("flowdrop: %s %s: %w", req.Method, req.URL.Path, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	e := responseError(resp)
	if resp.StatusCode == http.StatusUnauthorized {
		// Keep errors.Is(err, ErrUnauthorized) usable for callers.
		return nil, fmt.Errorf("%w: %s", ErrUnauthorized, e.Message)
	}
	return nil, e
}

// responseError decodes the Hub's uniform {"error":{code,message}} body. It
// never includes request headers, so the device token cannot leak.
func responseError(resp *http.Response) *Error {
	e := &Error{Status: resp.StatusCode}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil || len(b) == 0 {
		e.Message = http.StatusText(resp.StatusCode)
		return e
	}
	var wire struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &wire); err != nil || wire.Error.Code == "" {
		e.Message = http.StatusText(resp.StatusCode)
		return e
	}
	e.Code = wire.Error.Code
	e.Message = wire.Error.Message
	return e
}

func decodeItem(resp *http.Response) (Item, error) {
	var it Item
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBodyBytes)).Decode(&it); err != nil {
		return Item{}, fmt.Errorf("flowdrop: decode item response: %w", err)
	}
	if it.ID == "" {
		return Item{}, errors.New("flowdrop: hub returned an item without an id")
	}
	return it, nil
}

// cookieValue returns the value of a cookie the Hub set on the hub URL.
func (c *Client) cookieValue(name string) string {
	u, err := url.Parse(c.baseURL)
	if err != nil || c.http.Jar == nil {
		return ""
	}
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
