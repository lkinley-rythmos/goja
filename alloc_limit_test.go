package goja

import (
	"errors"
	"testing"
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
