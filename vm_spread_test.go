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

func TestSpreadCallAllocationLimit(t *testing.T) {
	// The growth of the value stack that a spread's values, or apply's
	// list, take it to, and the entries of an arguments object, are charged
	// to the allocation limit: 30 million spread values reached 1.8 GB (3.2
	// GB with an arguments object) with nothing charged, and apply of a
	// dense array into a native function 1.6 GB, where apply of an array-like
	// stops at the limit (createListFromArrayLike charges its list).
	dense := `var a = new Array(5e6).fill(1)`
	for _, tc := range []struct{ name, setup, script string }{
		{"spread", ``, `function g() {} g(...new Array(3e7))`},
		{"spread arguments", ``, `function f() { return arguments.length } f(...new Array(3e7))`},
		{"spread strict arguments", ``, `function f() { 'use strict'; return arguments.length } f(...new Array(3e7))`},
		{"spread native", ``, `Math.max(...new Array(3e7))`},
		// The array is made before the limit is set; apply takes a dense
		// array's values as they are.
		{"apply native", dense, `Math.max.apply(null, a)`},
		{"apply", dense, `function g() {} g.apply(null, a)`},
		{"Reflect.apply", dense, `function g() {} Reflect.apply(g, null, a)`},
		{"apply arguments", `var a = []; for (var i = 0; i < 3e6; i++) a.push(i)`, `function f() { return arguments.length } for (var i = 0; i < 10; i++) f.apply(null, a)`},
		{"apply strict arguments", `var a = []; for (var i = 0; i < 3e6; i++) a.push(i)`, `function f() { 'use strict'; return arguments.length } for (var i = 0; i < 10; i++) f.apply(null, a)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := New()
			if _, err := vm.RunString(tc.setup); err != nil {
				t.Fatal(err)
			}
			vm.SetAllocationLimit(64 << 20)
			timer := time.AfterFunc(20*time.Second, func() { vm.Interrupt("halt") })
			defer timer.Stop()
			_, err := vm.RunString(tc.script)
			if !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("err %v, %d MB charged", err, vm.AllocatedBytes()>>20)
			}
		})
	}
	// Under the limit the same calls run, and a stack that has grown is
	// not charged again: a million spreads of ten values charged 160 MB.
	vm := New()
	vm.SetAllocationLimit(64 << 20)
	v, err := vm.RunString(`function f() { return arguments.length } function g(a, b) { return b }
		[f(...new Array(1e5)), g(...new Array(1e5).fill(7)), f.apply(null, new Array(1e5))].join()`)
	if err != nil || v.String() != "100000,7,100000" {
		t.Fatalf("%v %v", v, err)
	}
	vm = New()
	vm.SetAllocationLimit(64 << 20)
	v, err = vm.RunString(`var a = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10], s = 0;
		function g(x) { return x }
		for (var i = 0; i < 1e6; i++) s += Math.max(...a) + Math.max.apply(null, a) + g(...a) + Reflect.apply(g, null, a);
		s`)
	if err != nil || v.String() != "22000000" {
		t.Fatalf("%v %v, %d MB charged", v, err, vm.AllocatedBytes()>>20)
	}
}
