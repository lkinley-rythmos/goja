package goja

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// compileWithin compiles src and fails unless it finishes within limit and
// allocates less than maxAlloc bytes. It runs the compile in a goroutine so
// that a compile that does not finish fails the test instead of hanging it.
func compileWithin(t *testing.T, src string, limit time.Duration, maxAlloc uint64) *Program {
	t.Helper()
	type result struct {
		prg   *Program
		err   error
		alloc uint64
		took  time.Duration
	}
	done := make(chan result, 1)
	go func() {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		prg, err := Compile("", src, false)
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		done <- result{prg, err, after.TotalAlloc - before.TotalAlloc, took}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("compile error: %v", r.err)
		}
		if r.took > limit {
			t.Fatalf("compile took %v, want under %v", r.took, limit)
		}
		if r.alloc > maxAlloc {
			t.Fatalf("compile allocated %d bytes, want under %d", r.alloc, maxAlloc)
		}
		return r.prg
	case <-time.After(limit):
		t.Fatalf("compile did not finish within %v", limit)
	}
	return nil
}

// Evaluating a constant logical expression evaluated its left operand again
// at every level, so a chain of n terms took 2^n steps: 26 took 9 s.
func TestConstFoldLogicalChainLinear(t *testing.T) {
	for _, tc := range []struct{ head, term string }{
		{"3n", " || 0n"},
		{"3n", " ?? 0n"},
		{"0n", " && 0n"},
		{"1", " && 1 || 0"},
		{"null", " ?? undefined"},
	} {
		for _, n := range []int{26, 2000} {
			src := "function f() { return " + tc.head + strings.Repeat(tc.term, n) + "; }"
			t.Run(fmt.Sprintf("%s%s x%d", tc.head, tc.term, n), func(t *testing.T) {
				compileWithin(t, src, time.Second, 64<<20)
			})
		}
	}
	// Nested on the right as well as on the left.
	src := "function f() { return " + strings.Repeat("(0n || ", 1000) + "1n" + strings.Repeat(")", 1000) + "; }"
	compileWithin(t, src, time.Second, 64<<20)
}
