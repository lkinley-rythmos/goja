package goja

import (
	"errors"
	"unsafe"

	"github.com/dop251/goja/unistring"
)

// ErrAllocationLimitExceeded is the value carried by the *InterruptedError
// that a Runtime raises when its allocation counter passes the limit set with
// SetAllocationLimit. errors.Is(err, ErrAllocationLimitExceeded) reports
// whether a run was stopped for that reason.
var ErrAllocationLimitExceeded = errors.New("goja: allocation limit exceeded")

// allocCheckInterval is the granularity of limit checks: the limit is only
// compared when the counter crosses a multiple of this many bytes, so a run
// can overshoot the limit by up to this amount.
const allocCheckInterval = 1 << 20

// Estimated sizes charged per allocation. They count the object header and
// the storage the counted constructor itself creates, not later growth.
const (
	allocSizeBaseObject = int64(unsafe.Sizeof(baseObjectBox{}))
	allocSizeArray      = int64(unsafe.Sizeof(arrayObjectBox{}))
	allocSizeValue      = int64(unsafe.Sizeof(Value(nil)))
	// One pre-sized string-keyed property: a map key and value plus the
	// propNames entry.
	allocSizePropSlot = 2*int64(unsafe.Sizeof(unistring.String(""))) + allocSizeValue
	// An entry of an arguments object: a property slot and its key, the
	// index's digits (up to 8 for the counts a value stack holds).
	allocSizeArgsEntry = allocSizePropSlot + 8
	// An element of a sparse array.
	allocSizeSparseItem = int64(unsafe.Sizeof(sparseArrayItem{}))
)

type allocCounter struct {
	bytes     int64 // estimated bytes allocated since the last SetAllocationLimit
	limit     int64 // 0 disables the limit
	nextCheck int64 // next multiple of allocCheckInterval at which to check
}

// SetAllocationLimit resets the allocation counter to zero and sets the
// limit in estimated bytes; bytes <= 0 disables the limit (counting
// continues). The counter covers ordinary objects, arrays and their storage
// as it grows, ArrayBuffers and typed arrays, strings created by the +
// operator and JSON.parse, BigInts made by the operators and by
// BigInt.asIntN and asUintN, and the value stack's growth, and nothing else,
// so it is an estimate meant to stop runaway scripts, not an exact
// accounting.
//
// When the counter passes the limit while JavaScript is running, the run
// stops with an *InterruptedError whose Value() is ErrAllocationLimitExceeded.
// The limit is checked only when the counter crosses a 1 MiB boundary, so the
// overshoot is at most about 1 MiB plus one allocation. Allocations made while
// no JavaScript is running are counted but never raise; the next boundary
// crossed inside a run does.
func (r *Runtime) SetAllocationLimit(bytes int64) {
	r.alloc = allocCounter{limit: max(bytes, 0)}
	// A new run starts: what the last one built its strings in is released.
	r.appendBufs.release()
}

// AllocatedBytes returns the estimated bytes counted since the Runtime was
// created or since the last SetAllocationLimit call.
func (r *Runtime) AllocatedBytes() int64 {
	return r.alloc.bytes
}

// chargeValues charges a slice of n values that an array's storage is made
// of, when it is made: as an array grows, each larger slice it moves to is
// charged, as the value stack's are (see expandCharged), since the ones it
// leaves are only reclaimed later (a push loop of 3e7 values under a 64 MB
// limit reached 336 MB when only the capacity added was charged). Nothing
// else charges the slice, nor a value put in it.
func (r *Runtime) chargeValues(n int) {
	r.countAlloc(int64(n) * allocSizeValue)
}

// appendValue appends v to values and charges the slice append moves them
// to (see chargeValues), for the values of an array made as they come (a
// spread's, an iterator's).
func (r *Runtime) appendValue(values []Value, v Value) []Value {
	c := cap(values)
	values = append(values, v)
	if c1 := cap(values); c1 != c {
		r.chargeValues(c1)
	}
	return values
}

// makeValues makes a slice of values for an array's storage, charged
// before it is made.
func (r *Runtime) makeValues(length, capacity int64) []Value {
	r.chargeValues(int(capacity))
	return make([]Value, length, capacity)
}

// countAlloc charges n estimated bytes. It is small enough to inline; the
// boundary work is out of line.
func (r *Runtime) countAlloc(n int64) {
	r.alloc.bytes += n
	if r.alloc.bytes >= r.alloc.nextCheck {
		r.allocBoundary()
	}
}

//go:noinline
func (r *Runtime) allocBoundary() {
	a := &r.alloc
	a.nextCheck = (a.bytes/allocCheckInterval + 1) * allocCheckInterval
	if a.limit == 0 || a.bytes <= a.limit {
		return
	}
	// Only raise inside a protected run (RunProgram, RunString, a Callable
	// from AssertFunction, ...). Each of those pushes a try frame that turns
	// the uncatchable panic into a returned error; outside one, the panic
	// would escape into host code.
	if r.vm == nil || len(r.vm.tryStack) == 0 {
		return
	}
	ex := &InterruptedError{iface: ErrAllocationLimitExceeded}
	ex.stack = r.vm.captureStack(nil, 0)
	panic(ex)
}

// maxStringLength is the longest string, in UTF-16 code units, that the
// built-ins produce: V8's String::kMaxLength on 64-bit platforms
// (2^29 - 24), past which Node throws "RangeError: Invalid string length".
// Without it, a single call such as "a".repeat(2**40) asks Go for a buffer
// so large that the allocation aborts the process.
const maxStringLength = 1<<29 - 24

//go:noinline
func (r *Runtime) throwInvalidStringLength() {
	panic(r.newError(r.getRangeError(), "Invalid string length"))
}

// growString is called before n more code units are appended to a string
// result that is cur units long. It refuses a result longer than
// maxStringLength and charges the n units to the allocation counter, the
// same estimate the + operator charges, so that the allocation limit refuses
// the result before it is allocated.
func (r *Runtime) growString(cur, n int) {
	if n > maxStringLength-cur {
		r.throwInvalidStringLength()
	}
	r.countAlloc(int64(n))
}

// chargeStringGrowth is growString for a result built by code that does not
// know the length of what it appends in advance: called after each step, it
// charges what the result grew by since *charged and refuses it past
// maxStringLength. The overshoot is at most one step.
func (r *Runtime) chargeStringGrowth(charged *int, l int) {
	if l > *charged {
		r.growString(*charged, l-*charged)
		*charged = l
	}
}
