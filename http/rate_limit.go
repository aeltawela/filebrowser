package fbhttp

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// attemptTracker bounds failed authentication attempts per key to blunt
// credential stuffing (login) and share-password guessing. After limit
// failures inside window, further attempts are refused until the window
// lapses. Keys are cheap strings (client IP, optionally scoped to a share);
// expired buckets are pruned on access and the table is capped so distinct
// keys cannot grow it without bound.
//
// The key deliberately uses the TCP peer address (RemoteAddr), never
// X-Forwarded-For or similar client-controlled headers: those are not trust
// anchors (see auth.ProxyAuth). Behind a proxy every client shares the
// proxy's address, so proxy-level limiting (e.g. fail2ban, as documented)
// remains the primary control; this is the in-application second layer.
type attemptTracker struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	buckets map[string]*attemptBucket
}

type attemptBucket struct {
	count int
	start time.Time
}

// maxTrackedKeys caps the bucket table. Attackers cycling keys only evict
// each other once past the cap; legitimate clients re-enter cleanly.
const maxTrackedKeys = 10000

func newAttemptTracker(limit int, window time.Duration) *attemptTracker {
	return &attemptTracker{
		limit:   limit,
		window:  window,
		buckets: make(map[string]*attemptBucket),
	}
}

// blocked reports whether key exhausted its budget. It never records.
func (t *attemptTracker) blocked(key string) bool {
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	b, ok := t.buckets[key]
	if !ok {
		return false
	}
	if now.Sub(b.start) >= t.window {
		delete(t.buckets, key)
		return false
	}
	return b.count >= t.limit
}

// record counts one failed attempt for key.
func (t *attemptTracker) record(key string) {
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.buckets) >= maxTrackedKeys {
		for k, b := range t.buckets {
			if now.Sub(b.start) >= t.window {
				delete(t.buckets, k)
			}
		}
	}

	b, ok := t.buckets[key]
	if !ok || now.Sub(b.start) >= t.window {
		t.buckets[key] = &attemptBucket{count: 1, start: now}
		return
	}
	b.count++
}

// reset clears all buckets. Tests use it to isolate rate-limit cases.
func (t *attemptTracker) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buckets = make(map[string]*attemptBucket)
}

// loginAttempts and sharePasswordAttempts mirror the FilebrowserNext policy:
// 10 failures per key per 5 minutes, then HTTP 429.
var (
	loginAttempts         = newAttemptTracker(10, 5*time.Minute)
	sharePasswordAttempts = newAttemptTracker(10, 5*time.Minute)
)

// clientIP returns the TCP peer address of the request. Forwarded headers
// are deliberately ignored: they are client-controlled and must never feed
// a security decision.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
