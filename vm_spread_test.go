package goja

import (
	"errors"
	"testing"
	"time"
)

func TestValueStackGrowsGeometrically(t *testing.T) {
	// A spread pushes its values one by one; the stack grew 1,024 slots at
	// a time, and copied itself quadratically (4 million values took 93 s).
	var s valueStack
	grows := 0
	for i := 0; i < 4000000; i++ {
		c := cap(s)
		s.expand(i)
		if cap(s) != c {
			grows++
		}
	}
	if grows > 60 {
		t.Errorf("grew %d times", grows)
	}
	if cap(s) > maxValueStackSlots {
		t.Errorf("capacity %d", cap(s))
	}
}

func TestSpreadCallLinear(t *testing.T) {
	// Spreading 4 million values into a call, with and without an
	// arguments object (which took 93 s).
	if raceDetectorEnabled {
		t.Skip("timing")
	}
	vm := New()
	start := time.Now()
	v, err := vm.RunString(`function f() { return arguments.length; } function g(a, b) { return b; }
		[f(...new Array(4e6)), g(...new Array(4e6).fill(7))].join()`)
	if err != nil || v.String() != "4000000,7" {
		t.Fatalf("%v %v", v, err)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestSpreadCallInterrupt(t *testing.T) {
	// An Interrupt stops a spread of millions of values, whether it comes
	// while they are pushed or while the arguments object is built.
	for _, tc := range []struct {
		name, script string
		after        func(spread time.Duration) time.Duration // when the Interrupt comes
	}{
		{"spread", `g(...a)`, func(time.Duration) time.Duration { return 50 * time.Millisecond }},
		// After the spread, while the arguments object is built.
		{"arguments", `f(...a)`, func(spread time.Duration) time.Duration { return spread + spread/4 + 50*time.Millisecond }},
		// A strict function's arguments object is unmapped, and built apart.
		{"strict arguments", `h(...a)`, func(spread time.Duration) time.Duration { return spread + spread/4 + 50*time.Millisecond }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := New()
			if _, err := vm.RunString(`var a = new Array(4e6); function f() { return arguments.length; } function g() { return 0; }
				function h() { 'use strict'; return arguments.length; }`); err != nil {
				t.Fatal(err)
			}
			// How long the spread alone takes, once the stack has grown.
			var spread time.Duration
			for i := 0; i < 2; i++ {
				start := time.Now()
				if _, err := vm.RunString(`g(...a)`); err != nil {
					t.Fatal(err)
				}
				spread = time.Since(start)
			}
			after := tc.after(spread)
			timer := time.AfterFunc(after, func() { vm.Interrupt("halt") })
			defer timer.Stop()
			start := time.Now()
			_, err := vm.RunString(tc.script)
			var ie *InterruptedError
			if !errors.As(err, &ie) {
				t.Fatalf("err %v (took %v, spread %v)", err, time.Since(start), spread)
			}
			if d := time.Since(start) - after; d > spread/3+50*time.Millisecond {
				t.Errorf("stopped %v after the Interrupt (spread %v)", d, spread)
			}
		})
	}
}
