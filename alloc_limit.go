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
)

type allocCounter struct {
	bytes     int64 // estimated bytes allocated since the last SetAllocationLimit
	limit     int64 // 0 disables the limit
	nextCheck int64 // next multiple of allocCheckInterval at which to check
}

// SetAllocationLimit resets the allocation counter to zero and sets the
// limit in estimated bytes; bytes <= 0 disables the limit (counting
// continues). The counter covers ordinary objects, arrays, strings created by
// the + operator and JSON.parse, and nothing else, so it is an estimate
// meant to stop runaway scripts, not an exact accounting.
//
// When the counter passes the limit while JavaScript is running, the run
// stops with an *InterruptedError whose Value() is ErrAllocationLimitExceeded.
// The limit is checked only when the counter crosses a 1 MiB boundary, so the
// overshoot is at most about 1 MiB plus one allocation. Allocations made while
// no JavaScript is running are counted but never raise; the next boundary
// crossed inside a run does.
func (r *Runtime) SetAllocationLimit(bytes int64) {
	r.alloc = allocCounter{limit: max(bytes, 0)}
}

// AllocatedBytes returns the estimated bytes counted since the Runtime was
// created or since the last SetAllocationLimit call.
func (r *Runtime) AllocatedBytes() int64 {
	return r.alloc.bytes
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
