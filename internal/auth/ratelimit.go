package auth

import (
	"sync"
	"time"
)

// Limiter allows `max` failures per key inside a sliding `window`.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *Limiter) prune(key string, now time.Time) []time.Time {
	h := l.hits[key]
	i := 0
	for i < len(h) && now.Sub(h[i]) > l.window {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
	return h
}

// Blocked reports whether the key has used up its failures.
func (l *Limiter) Blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, time.Now())) >= l.max
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.record(key, time.Now())
}

// Take reserves an attempt before the work is done: it counts the attempt and
// reports false when the key is already used up. Checking and counting in one
// step stops concurrent requests from all slipping past Blocked. Call Reset
// on success.
func (l *Limiter) Take(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.prune(key, now)) >= l.max {
		return false
	}
	l.record(key, now)
	return true
}

// record adds a hit. Caller holds l.mu.
func (l *Limiter) record(key string, now time.Time) {
	if len(l.hits) > 10_000 { // keep memory bounded under a spray of IPs
		for k := range l.hits {
			l.prune(k, now)
		}
	}
	l.hits[key] = append(l.prune(key, now), now)
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
