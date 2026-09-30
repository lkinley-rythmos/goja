package goja

import (
	"errors"
	"testing"
	"time"
)

func TestAllocationCounterCountsObjectsArraysAndStrings(t *testing.T) {
	r := New()
	r.SetAllocationLimit(0)
	if got := r.AllocatedBytes(); got != 0 {
		t.Fatalf("after reset: %d", got)
	}
	if _, err := r.RunString(`for (let i = 0; i < 1000; i++) { ({a: i, b: i}); }`); err != nil {
		t.Fatal(err)
	}
	objects := r.AllocatedBytes()
	if min := 1000 * (allocSizeBaseObject + 2*allocSizePropSlot); objects < min {
		t.Fatalf("objects: counted %d, want at least %d", objects, min)
	}

	r.SetAllocationLimit(0)
	if _, err := r.RunString(`for (let i = 0; i < 1000; i++) { [i, i, i]; }`); err != nil {
		t.Fatal(err)
	}
	if min := 1000 * (allocSizeArray + 3*allocSizeValue); r.AllocatedBytes() < min {
		t.Fatalf("arrays: counted %d, want at least %d", r.AllocatedBytes(), min)
	}

	r.SetAllocationLimit(0)
	if _, err := r.RunString(`let s = ""; for (let i = 0; i < 1000; i++) { s += "abcdefgh"; }`); err != nil {
		t.Fatal(err)
	}
	// Each += below appendMinLength charges the length of its result
	// (8+16+...+256); past it, the string grows in a buffer, and the
	// buffers are charged: at least the 8,000 bytes the string holds.
	if min := int64(8*32*33/2 + 8000); r.AllocatedBytes() < min {
		t.Fatalf("concat: counted %d, want at least %d", r.AllocatedBytes(), min)
	}

	r.SetAllocationLimit(0)
	if _, err := r.RunString(`JSON.parse('["abcdefghij", "klmnopqrst"]')`); err != nil {
		t.Fatal(err)
	}
	if min := int64(20) + allocSizeArray; r.AllocatedBytes() < min {
		t.Fatalf("JSON.parse: counted %d, want at least %d", r.AllocatedBytes(), min)
	}
}

func assertAllocLimitError(t *testing.T, err error) {
	t.Helper()
	var ie *InterruptedError
	if !errors.As(err, &ie) {
		t.Fatalf("want *InterruptedError, got %T: %v", err, err)
	}
	if ie.Value() != ErrAllocationLimitExceeded || !errors.Is(err, ErrAllocationLimitExceeded) {
		t.Fatalf("want the allocation-limit sentinel, got %v", ie.Value())
	}
}

func TestAllocationLimitStopsRunawayScript(t *testing.T) {
	r := New()
	const limit = 4 << 20
	r.SetAllocationLimit(limit)
	_, err := r.RunString(`
	let keep = [];
	for (;;) { keep.push({a: 1, b: 2, c: 3}); }
	`)
	assertAllocLimitError(t, err)
	if got := r.AllocatedBytes(); got <= limit || got > limit+allocCheckInterval+allocSizeBaseObject+64*allocSizePropSlot {
		t.Fatalf("stopped at %d bytes, want just past %d", got, limit)
	}

	// The runtime is usable again once the counter is reset.
	r.SetAllocationLimit(limit)
	v, err := r.RunString(`[1, 2, 3].map(x => ({x})).length`)
	if err != nil || v.ToInteger() != 3 {
		t.Fatalf("after reset: %v, %v", v, err)
	}
}

func TestAllocationLimitIsUncatchable(t *testing.T) {
	r := New()
	r.SetAllocationLimit(2 << 20)
	_, err := r.RunString(`
	let caught = false;
	try { for (;;) { [1, 2, 3, 4]; } } catch (e) { caught = true; } finally { caught = true; }
	caught;
	`)
	assertAllocLimitError(t, err)
	if v := r.Get("caught"); v != nil && v.ToBoolean() {
		t.Fatal("the allocation limit was caught by JavaScript")
	}
}

func TestAllocationLimitThroughCallable(t *testing.T) {
	r := New()
	if _, err := r.RunString(`function grow() { let a = []; for (;;) { a.push({}); } }`); err != nil {
		t.Fatal(err)
	}
	grow, ok := AssertFunction(r.Get("grow"))
	if !ok {
		t.Fatal("grow is not a function")
	}
	r.SetAllocationLimit(2 << 20)
	_, err := grow(Undefined())
	assertAllocLimitError(t, err)
}

func TestAllocationLimitNotRaisedOutsideRun(t *testing.T) {
	r := New()
	r.SetAllocationLimit(1)
	// Host-side allocation past the limit must not panic into Go code.
	for range 2 * allocCheckInterval / int(allocSizeBaseObject) {
		r.NewObject()
	}
	if r.AllocatedBytes() <= allocCheckInterval {
		t.Fatalf("host allocations not counted: %d", r.AllocatedBytes())
	}
	// The next boundary crossed inside a run raises.
	_, err := r.RunString(`for (;;) { ({}); }`)
	assertAllocLimitError(t, err)
}

func TestAllocationLimitUnderLimitRunsNormally(t *testing.T) {
	r := New()
	r.SetAllocationLimit(64 << 20)
	v, err := r.RunString(`let n = 0; for (let i = 0; i < 10000; i++) { n += [{v: i}][0].v; } n`)
	if err != nil || v.ToInteger() != 49995000 {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestArrayGrowthAllocationLimit(t *testing.T) {
	// An array's storage is charged as it grows, as are the arrays the
	// built-ins make and ArrayBuffers: with a 64 MB limit, new
	// Array(3e7).fill(1) completed at 1.8 GB, and a push loop of 3e7
	// values at 1.5 GB, with nothing charged. Each script stops at the
	// limit, the setup (under no limit) made before it.
	dense := `var a = new Array(5e6).fill(1)`
	for _, tc := range []struct{ name, setup, script string }{
		{"fill", ``, `new Array(3e7).fill(1)`},
		{"push", ``, `var a = []; for (var i = 0; i < 3e7; i++) a.push(i)`},
		{"store", ``, `var a = []; for (var i = 0; i < 3e7; i++) a[i] = i`},
		{"length and store", ``, `var a = []; a.length = 3e7; for (var i = 0; i < 3e7; i++) a[i] = i`},
		{"sparse", ``, `var a = []; for (var i = 0; i < 3e7; i++) a[i * 100] = i`},
		{"unshift", `var a = [1], b = new Array(4e5).fill(2)`, `for (var i = 0; i < 50; i++) a.unshift(...b)`},
		{"splice", `var a = [1], b = new Array(4e5).fill(2)`, `for (var i = 0; i < 50; i++) a.splice(1, 0, ...b)`},
		{"spread into an array", ``, `[...new Array(3e7)]`},
		{"spread of a generator", ``, `function* g() { for (;;) yield 1 } [0, ...g()]`},
		{"array destructuring rest", ``, `function* g() { for (;;) yield 1 } var [x, ...rest] = g()`},
		{"Array.from an iterator", ``, `function* g() { for (;;) yield 1 } Array.from(g())`},
		{"Array.from an array-like", ``, `Array.from({length: 3e7})`},
		{"concat", dense, `a.concat(a, a, a, a, a, a, a)`},
		{"map", dense, `for (var i = 0; i < 10; i++) a = a.map(x => x)`},
		{"filter", dense, `a.concat(a, a, a).filter(x => true)`},
		{"slice", dense, `var k = []; for (var i = 0; i < 10; i++) k.push(a.slice())`},
		{"toReversed", dense, `var k = []; for (var i = 0; i < 10; i++) k.push(a.toReversed())`},
		{"toSorted", dense, `var k = []; for (var i = 0; i < 10; i++) k.push(a.toSorted())`},
		{"apply of a built array", ``, `Math.max.apply(null, new Array(3e7).fill(1))`},
		{"Reflect.apply of a built array", ``, `Reflect.apply(Math.max, null, new Array(3e7).fill(1))`},
		{"ArrayBuffer", ``, `new ArrayBuffer(1e8)`},
		{"Uint8Array", ``, `new Uint8Array(1e8)`},
		{"Float64Array", ``, `new Float64Array(2e7)`},
		{"typed arrays in a loop", ``, `var k = []; for (;;) k.push(new Uint8Array(1 << 16))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := New()
			if _, err := vm.RunString(tc.setup); err != nil {
				t.Fatal(err)
			}
			vm.SetAllocationLimit(64 << 20)
			timer := time.AfterFunc(30*time.Second, func() { vm.Interrupt("halt") })
			defer timer.Stop()
			_, err := vm.RunString(tc.script)
			if !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("err %v, %d MB charged", err, vm.AllocatedBytes()>>20)
			}
		})
	}
}

func TestArrayGrowthCharged(t *testing.T) {
	// Each slice an array's storage moves to as it grows is charged once,
	// and nothing else: an array of a million values pushed one by one
	// moves through slices that sum to about five times its storage (16 MB,
	// 8 MB with 8-byte values; the slices grow by a quarter). A spread of it
	// into a call charges the stack's growth, once, and not an array.
	vm := New()
	vm.SetAllocationLimit(0)
	if _, err := vm.RunString(`var a = []; for (var i = 0; i < 1e6; i++) a.push(i)`); err != nil {
		t.Fatal(err)
	}
	storage := 1e6 * allocSizeValue
	if got := vm.AllocatedBytes(); got < 4*storage || got > 6*storage {
		t.Fatalf("push: %d bytes charged, want %d to %d", got, 4*storage, 6*storage)
	}
	vm.SetAllocationLimit(0)
	if _, err := vm.RunString(`function f() {} f(...a)`); err != nil {
		t.Fatal(err)
	}
	once := vm.AllocatedBytes()
	vm.SetAllocationLimit(0)
	if _, err := vm.RunString(`for (var i = 0; i < 10; i++) f(...a)`); err != nil {
		t.Fatal(err)
	}
	// The stack a run leaves is not kept: ten spreads grow it once.
	if got := vm.AllocatedBytes(); got > once+once/10 {
		t.Fatalf("spread: ten charged %d bytes, one %d", got, once)
	}
	// Under a 64 MB limit, arrays of 200,000 values run as before.
	vm = New()
	vm.SetAllocationLimit(64 << 20)
	v, err := vm.RunString(`var a = []; for (var i = 0; i < 2e5; i++) a.push(i);
		var b = a.map(x => x * 2).filter(x => x % 4 == 0); var c = [...b, ...b];
		[a.length, b.length, c.length, new Uint8Array(1 << 20).length, Array.from(a.values()).length].join()`)
	if err != nil || v.String() != "200000,100000,200000,1048576,200000" {
		t.Fatalf("%v %v, %d MB charged", v, err, vm.AllocatedBytes()>>20)
	}
}
