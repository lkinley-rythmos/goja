package goja

import (
	"sync/atomic"

	"github.com/dop251/goja/unistring"
)

// maxNativeDepth bounds how many levels of Go recursion the runtime performs
// on behalf of a script. JavaScript-to-JavaScript calls do not recurse in Go
// (the vm runs them in a loop) and are bounded by SetMaxCallStackSize. Every
// other recursion does consume the Go stack: a nested object walked by
// JSON.stringify, Array.prototype.join/toString/toLocaleString/flat and the
// JSON.parse reviver, a chain of prototypes, proxies or bound functions, a
// native function calling another callable (a getter, toString, valueOf, ...),
// and every call from Go back into JavaScript. Go aborts the whole process
// with "fatal error: stack overflow" when a goroutine's stack passes its
// limit (1 GB on 64-bit platforms), so each of those levels is counted here
// and, past the bound, the runtime throws a catchable
// "RangeError: Maximum call stack size exceeded", which is what V8 throws
// when its own (native) stack runs out.
//
// The bound is a constant rather than a function of SetMaxCallStackSize:
// that limit counts JavaScript frames, which cost no Go stack, and its
// default (math.MaxInt32) would leave the Go stack unprotected. 10000 levels
// use a few tens of MB of Go stack at most, far below Go's limit; it is also
// the nesting limit of encoding/json on toolchains that enforce one, and it
// is above the depths at which Node 18 throws for the same walks
// (JSON.stringify ~7000, join ~2100, flat ~9000, native-to-JS reentry ~4000).
const maxNativeDepth = 10000

const errMsgStackOverflow = "Maximum call stack size exceeded"

// enterNative counts one level of Go recursion and throws a RangeError when
// the bound is passed. Every enterNative must be paired with leaveNative on
// the normal return path. When a panic unwinds past the pair instead, the
// try frame that catches it restores the depth (see pushTryFrame and
// handleThrow), so a defer is not required.
func (vm *vm) enterNative() {
	vm.nativeDepth++
	if vm.nativeDepth > maxNativeDepth {
		vm.nativeDepthExceeded()
	}
}

func (vm *vm) leaveNative() {
	vm.nativeDepth--
}

// nativeDepthExceeded throws the RangeError as an *Exception, the panic value
// the host API documents for JavaScript exceptions, since some of the counted
// recursions (Export, ExportTo, MarshalJSON) run outside the vm.
//
//go:noinline
func (vm *vm) nativeDepthExceeded() {
	vm.nativeDepth--
	panic(&Exception{
		val: vm.r.newError(vm.r.getRangeError(), errMsgStackOverflow),
	})
}

func (r *Runtime) enterNative() {
	r.vm.enterNative()
}

func (r *Runtime) leaveNative() {
	r.vm.leaveNative()
}

// interruptCheckInterval is how many iterations a long native loop runs
// between checks for a pending Runtime.Interrupt.
const interruptCheckInterval = 1024

// lengthCapHint is the capacity to preallocate for a list that will hold up
// to n values read from an object's "length". A script chooses that length
// (up to 2^53-1, for any array-like or a sparse array), so preallocating all
// of it can exhaust memory, or panic in make, before a single element is
// read; beyond this hint the list grows as elements are appended.
func lengthCapHint(n int64) int {
	const maxHint = 1 << 16
	if n > maxHint {
		return maxHint
	}
	if n < 0 {
		return 0
	}
	return int(n)
}

// checkInterrupt raises the pending interrupt, if any, from a long-running
// native loop, the same way the vm's run loop raises it between
// instructions. Like the allocation limit it only raises inside a protected
// run; outside one the panic would escape into host code, and the interrupt
// stays pending for the next run instead.
func (vm *vm) checkInterrupt() {
	if atomic.LoadUint32(&vm.interrupted) != 0 {
		vm.raiseInterrupt()
	}
}

func (r *Runtime) checkInterrupt() {
	r.vm.checkInterrupt()
}

//go:noinline
func (vm *vm) raiseInterrupt() {
	if len(vm.tryStack) == 0 {
		return
	}
	vm.interruptLock.Lock()
	v := &InterruptedError{
		iface: vm.interruptVal,
	}
	v.stack = vm.captureStack(nil, 0)
	vm.interruptLock.Unlock()
	panic(v)
}

// objectStack is the stack of objects a recursive walk is inside of, used to
// detect cycles. An object is on the stack if it is SameAs one of the items,
// which for most objects is identity, but for wrapped Go values compares
// what they wrap. Membership is a linear scan while the stack is shallow (the
// usual case, and cheaper than hashing); once it is deep, identity is a set
// lookup and only the wrapped Go values on the stack are scanned, so a walk
// of depth d costs O(d) rather than O(d^2).
type objectStack struct {
	items []*Object
	set   map[*Object]struct{}
	// custom holds the items, in stack order, whose equality is not
	// identity; it is only maintained while set is.
	custom []*Object
}

// objectStackScanLimit is the depth up to which membership is a linear scan.
const objectStackScanLimit = 32

// hasCustomEquality reports whether o's impl overrides equal (see
// StrictEquals), so that another *Object can be SameAs o. Ordinary objects
// compare by identity alone.
func hasCustomEquality(o *Object) bool {
	switch o.self.(type) {
	case reflectValueWrapper, *objectGoSlice, *taggedTemplateArray:
		return true
	}
	return false
}

// push adds o and reports true, or reports false without adding it when o is
// already on the stack.
func (s *objectStack) push(o *Object) bool {
	if s.set == nil {
		for _, item := range s.items {
			if item.SameAs(o) {
				return false
			}
		}
		s.items = append(s.items, o)
		if len(s.items) > objectStackScanLimit {
			s.set = make(map[*Object]struct{}, 2*len(s.items))
			for _, item := range s.items {
				s.set[item] = struct{}{}
				if hasCustomEquality(item) {
					s.custom = append(s.custom, item)
				}
			}
		}
		return true
	}
	if _, exists := s.set[o]; exists {
		return false
	}
	custom := hasCustomEquality(o)
	if custom {
		for _, item := range s.custom {
			if item.SameAs(o) {
				return false
			}
		}
		s.custom = append(s.custom, o)
	}
	s.set[o] = struct{}{}
	s.items = append(s.items, o)
	return true
}

func (s *objectStack) pop() {
	l := len(s.items) - 1
	o := s.items[l]
	s.items[l] = nil
	s.items = s.items[:l]
	if s.set != nil {
		delete(s.set, o)
		if c := len(s.custom) - 1; c >= 0 && s.custom[c] == o {
			s.custom[c] = nil
			s.custom = s.custom[:c]
		}
		if l <= objectStackScanLimit/2 {
			s.set = nil
			clear(s.custom)
			s.custom = s.custom[:0]
		}
	}
}

// Prototype-chain hops. A property lookup, has-check or assignment that
// misses an object's own properties continues in its prototype as a Go call,
// so a long prototype chain is deep Go recursion; each hop is counted.

func protoGet(proto *Object, p, receiver Value) Value {
	vm := proto.runtime.vm
	vm.enterNative()
	v := proto.get(p, receiver)
	vm.leaveNative()
	return v
}

func protoGetStr(proto *Object, name unistring.String, receiver Value) Value {
	vm := proto.runtime.vm
	vm.enterNative()
	v := proto.self.getStr(name, receiver)
	vm.leaveNative()
	return v
}

func protoGetIdx(proto *Object, idx valueInt, receiver Value) Value {
	vm := proto.runtime.vm
	vm.enterNative()
	v := proto.self.getIdx(idx, receiver)
	vm.leaveNative()
	return v
}

func protoGetSym(proto *Object, s *Symbol, receiver Value) Value {
	vm := proto.runtime.vm
	vm.enterNative()
	v := proto.self.getSym(s, receiver)
	vm.leaveNative()
	return v
}

func protoHasPropertyStr(proto *Object, name unistring.String) bool {
	vm := proto.runtime.vm
	vm.enterNative()
	res := proto.self.hasPropertyStr(name)
	vm.leaveNative()
	return res
}

func protoHasPropertyIdx(proto *Object, idx valueInt) bool {
	vm := proto.runtime.vm
	vm.enterNative()
	res := proto.self.hasPropertyIdx(idx)
	vm.leaveNative()
	return res
}

func protoHasPropertySym(proto *Object, s *Symbol) bool {
	vm := proto.runtime.vm
	vm.enterNative()
	res := proto.self.hasPropertySym(s)
	vm.leaveNative()
	return res
}

func protoSetForeignStr(proto *Object, name unistring.String, val, receiver Value, throw bool) (bool, bool) {
	vm := proto.runtime.vm
	vm.enterNative()
	res, handled := proto.self.setForeignStr(name, val, receiver, throw)
	vm.leaveNative()
	return res, handled
}

func protoSetForeignIdx(proto *Object, idx valueInt, val, receiver Value, throw bool) (bool, bool) {
	vm := proto.runtime.vm
	vm.enterNative()
	res, handled := proto.self.setForeignIdx(idx, val, receiver, throw)
	vm.leaveNative()
	return res, handled
}

func protoSetForeignSym(proto *Object, s *Symbol, val, receiver Value, throw bool) (bool, bool) {
	vm := proto.runtime.vm
	vm.enterNative()
	res, handled := proto.self.setForeignSym(s, val, receiver, throw)
	vm.leaveNative()
	return res, handled
}
