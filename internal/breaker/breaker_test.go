// Copyright 2026 Valdrent and the nextcloud-mcp-fast contributors
// SPDX-License-Identifier: Apache-2.0

package breaker

import (
	"fmt"
	"testing"
	"time"

	ncerr "github.com/valdrent/nextcloud-mcp-fast/internal/errors"
)

func TestBreakerTrips(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := New(3, time.Minute)
	b.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		b.Record("acct")
	}
	err := b.Check("acct")
	if !ncerr.Is(err, ncerr.CodeCircuitOpen) {
		t.Fatalf("want circuit_open, got %v", err)
	}

	// Other accounts unaffected.
	if err := b.Check("other"); err != nil {
		t.Fatalf("other account should not be tripped: %v", err)
	}

	// Window elapses -> clears.
	now = now.Add(2 * time.Minute)
	b.Record("acct") // prunes old entries, leaves 1
	if err := b.Check("acct"); err != nil {
		t.Fatalf("should be open again after window: %v", err)
	}
}

func TestBreakerForget(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := New(2, time.Minute)
	b.now = func() time.Time { return now }

	b.Record("a")
	b.Forget("a")
	b.Record("a")
	if err := b.Check("a"); err != nil {
		t.Fatalf("Forget should reset streak: %v", err)
	}
}

func TestBreakerDisabled(t *testing.T) {
	b := New(0, time.Minute)
	for i := 0; i < 100; i++ {
		b.Record("a")
	}
	if err := b.Check("a"); err != nil {
		t.Fatalf("disabled breaker must never trip: %v", err)
	}
}

func TestCheckDoesNotDoubleCount(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := New(3, 10*time.Second)
	b.now = func() time.Time { return now }
	b.Record("k") // t=0
	now = now.Add(6 * time.Second)
	b.Record("k") // t=6
	now = now.Add(1 * time.Second)
	b.Record("k")                  // t=7
	now = now.Add(4 * time.Second) // t=11: only t=6 and t=7 are recent
	for i := 0; i < 2; i++ {
		if err := b.Check("k"); err != nil {
			t.Fatalf("Check #%d: circuit open with only 2 recent failures: %v", i, err)
		}
	}
}

func TestKeyCapBounded(t *testing.T) {
	b := New(3, time.Minute)
	for i := 0; i < maxKeys+50; i++ {
		b.Record(fmt.Sprintf("k%d", i))
	}
	if len(b.fails) > maxKeys {
		t.Errorf("fails has %d keys, want <= %d", len(b.fails), maxKeys)
	}
}
