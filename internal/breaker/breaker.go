// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

// Package breaker implements a per-account sliding-window circuit breaker.
// It stops LLMs (or buggy clients) from hammering Nextcloud with failing
// calls: after N failures within the window, all further calls for that
// account are rejected until the window elapses.
package breaker

import (
	"sync"
	"time"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

// Breaker tracks failure counts per key (account ID).
type Breaker struct {
	mu        sync.Mutex
	threshold int
	window    time.Duration
	fails     map[string][]time.Time
	now       func() time.Time
}

// New creates a Breaker. threshold <= 0 disables it (Record is a no-op, Check
// always passes).
func New(threshold int, window time.Duration) *Breaker {
	return &Breaker{
		threshold: threshold,
		window:    window,
		fails:     make(map[string][]time.Time),
		now:       time.Now,
	}
}

// Check reports whether the key is currently tripped.
func (b *Breaker) Check(key string) error {
	if b.threshold <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	recent := b.recentLocked(key)
	b.storeLocked(key, recent)
	if len(recent) >= b.threshold {
		last := recent[len(recent)-1]
		resetAt := last.Add(b.window)
		return ncerr.New(ncerr.CodeCircuitOpen,
			"%d failed calls within the last %s; circuit open until %s. Wait before retrying.",
			len(recent), b.window, resetAt.Format(time.RFC3339))
	}
	return nil
}

// Record registers a failure for key and prunes old entries.
func (b *Breaker) Record(key string) {
	if b.threshold <= 0 {
		return
	}
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fails[key] = append(b.recentLocked(key), now)
	if len(b.fails) > maxKeys {
		b.evictLocked()
	}
}

// maxKeys bounds the number of tracked keys.
const maxKeys = 10000

// storeLocked saves the pruned history, deleting the key when it is empty.
func (b *Breaker) storeLocked(key string, recent []time.Time) {
	if len(recent) == 0 {
		delete(b.fails, key)
		return
	}
	b.fails[key] = recent
}

// evictLocked drops keys whose failures all expired; if still over the cap it
// drops arbitrary keys until back under it.
func (b *Breaker) evictLocked() {
	for k := range b.fails {
		b.storeLocked(k, b.recentLocked(k))
	}
	for k := range b.fails {
		if len(b.fails) <= maxKeys {
			return
		}
		delete(b.fails, k)
	}
}

// Forget clears the failure history for key (call after a successful call so
// one good request resets the streak).
func (b *Breaker) Forget(key string) {
	if b.threshold <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.fails, key)
}

func (b *Breaker) recentLocked(key string) []time.Time {
	cutoff := b.now().Add(-b.window)
	hist := b.fails[key]
	var out []time.Time
	for _, t := range hist {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}
