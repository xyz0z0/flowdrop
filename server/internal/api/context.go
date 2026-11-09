package api

import (
	"context"
	"sync"
	"time"
)

type ctxKey int

const sessionKey ctxKey = 1

// reqSession is the authenticated identity of one request. It is derived from a
// paired device token, never from a password or a stored session row.
type reqSession struct {
	// DeviceID is the paired device the request is authenticated as.
	DeviceID string
	// Token is the raw device token used for this request (cookie or Bearer).
	// It is never logged or echoed back.
	Token string
	// TokenHash is the SHA-256 of Token. It is only used as the rate-limiter
	// key so the raw credential is never held in a limiter bucket.
	TokenHash string
	// ExpiresAt is the lifetime of the session cookie issued for this identity.
	ExpiresAt time.Time
}

func withSession(ctx context.Context, s reqSession) context.Context {
	return context.WithValue(ctx, sessionKey, s)
}

func sessionFrom(ctx context.Context) (reqSession, bool) {
	v, ok := ctx.Value(sessionKey).(reqSession)
	return v, ok
}

// limiter is a fixed-window counter keyed by an arbitrary string. It is
// intentionally small: enough to stop pairing-code guessing and runaway
// clients.
type limiter struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	now    func() time.Time
	bucket map[string]*bucket
	calls  int
}

type bucket struct {
	start time.Time
	count int
}

func newLimiter(limit int, window time.Duration) *limiter {
	if limit <= 0 {
		limit = 1
	}
	return &limiter{
		window: window,
		limit:  limit,
		now:    time.Now,
		bucket: make(map[string]*bucket),
	}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	// Opportunistic sweep to keep the map bounded.
	l.calls++
	if l.calls%256 == 0 {
		for k, b := range l.bucket {
			if now.Sub(b.start) > l.window {
				delete(l.bucket, k)
			}
		}
	}

	b, ok := l.bucket[key]
	if !ok || now.Sub(b.start) > l.window {
		l.bucket[key] = &bucket{start: now, count: 1}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}

// reset clears a key's counter (e.g. after a successful login).
func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.bucket, key)
	l.mu.Unlock()
}
