package api

import (
	"sync"
	"time"
)

// attemptLimiter throttles password guessing and mass signup. Once registration
// is open, the login form is reachable by anyone who can reach the port, so a
// wrong password must not be free to try forever.
//
// It is deliberately in-memory and per client: a restart forgives, and a
// self-hosted server has no shared state to coordinate. The zero value allows
// everything, so a Server built without New() (as the API tests do) still works.
type attemptLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

// maxLimiterKeys bounds memory if the port is scanned from many addresses; the
// whole table is dropped rather than letting it grow without limit.
const maxLimiterKeys = 20000

func newAttemptLimiter(max int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{hits: map[string][]time.Time{}, max: max, window: window}
}

// Allow reports whether key may make another attempt right now.
func (l *attemptLimiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key)) < l.max
}

// Hit records one attempt and returns how many seconds the caller must wait
// before trying again (0 while it is still within the allowance).
func (l *attemptLimiter) Hit(key string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > maxLimiterKeys {
		l.hits = map[string][]time.Time{}
	}
	kept := append(l.recent(key), time.Now())
	l.hits[key] = kept
	if len(kept) < l.max {
		return 0
	}
	wait := l.window - time.Since(kept[0])
	if wait <= 0 {
		return 0
	}
	return int(wait.Seconds()) + 1
}

// Reset forgets a key's history after a successful sign-in, so a user who
// mistyped a few times is not still throttled once they get it right.
func (l *attemptLimiter) Reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

// recent returns the attempts still inside the window. Callers hold the lock.
func (l *attemptLimiter) recent(key string) []time.Time {
	now := time.Now()
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	l.hits[key] = kept
	return kept
}
