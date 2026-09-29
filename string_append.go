package goja

import (
	"slices"
	"unsafe"
	"weak"

	"github.com/dop251/goja/unistring"
)

// Concatenation into shared buffers.
//
// A script that builds a string with s += x copies s on every step, which is
// quadratic in the final length: a million one-character steps copy 500 GB.
// Instead, once a long string is seen growing, its concatenations leave their
// results in a buffer with room to grow, and the runtime remembers, for the
// buffer, how many code units its longest string uses. When the left operand
// of a later concatenation is that longest string (it starts at the buffer's
// start and is exactly that long), the right operand is copied into the room
// after it and the result is a longer view of the same buffer. The strings
// already made from the buffer only ever see the units before the ones
// written, so they never change; the result is an ordinary asciiString or
// unicodeString, and every other string operation is unaffected.
//
// The runtime finds a buffer from its operand's data pointer, in a map
// holding weak pointers, so that the map keeps no buffer alive: an entry
// whose buffer is gone is dropped, and a string at a reused address is never
// taken for a view of a dead buffer. Only the runtime's own goroutine writes
// to a buffer, and only past every string made from it, so strings passed to
// other goroutines stay immutable.
//
// A weak pointer costs an allocation, so a long concatenation first makes
// its result as before, at its exact size, and only marks it (its address
// and length) as possibly growing, with the steps of the chain that made
// it. A concatenation onto a string marked as the result of two steps is
// the third step of a string being built: its result goes into a buffer.
// Nothing is written through a mark, so a mark left by a string that is gone
// and matched by another string at the same address costs only a buffer. A
// mark is used once: the other concatenations onto the same string, forks
// of it, are made at their exact size.
//
// The steps of a chain are the concatenations whose left operand is a value
// that was kept: s += x is one each time. The later + of one expression,
// a + b + c, has for its left operand the one before's result, which
// nothing holds: it is part of the same step (concatStringChained), so an
// expression of any number of terms, made once, makes each of its results
// at its exact size, as base did; built on in a loop, it is promoted as
// s += x is, and its later + append in place.

// appendMinLength is the length, in code units, a left operand needs for its
// concatenation to be marked or to go through a shared buffer. Below it,
// copying costs less than looking the buffer up.
const appendMinLength = 256

// appendMarkSlots is the size of the table of marks, a power of two. A mark
// goes in the slot its address hashes to, and takes it from the one there:
// a mark lost only makes its string be promoted later, and the table costs
// no allocation per mark.
const appendMarkSlots = 1 << appendMarkBits

const appendMarkBits = 10

// appendPromoteSteps is the step of a chain of concatenations onto marked
// results at which the result goes into a buffer with room: the first two
// are made at their exact size, so that t = big + x; t += y costs what it
// always did.
const appendPromoteSteps = 3

type appendBuf struct {
	// One of them is set: the buffer is bytes of an asciiString, or the
	// code units of a unicodeString, BOM first.
	b weak.Pointer[byte]
	u weak.Pointer[uint16]
	n int // code units used, the BOM included
	c int // code units allocated, the BOM included
}

// appendMark is a result made at its exact size: its address, its length
// (the BOM included), and the steps of the chain that made it.
type appendMark struct {
	p     uintptr
	n     uint32
	steps uint32
}

type appendBufs struct {
	m       map[uintptr]appendBuf
	sweepAt int
	peak    int // the most entries m has had since it was made
	marks   *[appendMarkSlots]appendMark
}

// lookup returns the buffer whose longest string starts at p and is n code
// units long, if there is one.
func (a *appendBufs) lookup(p unsafe.Pointer, n int) (appendBuf, bool) {
	e, ok := a.m[uintptr(p)]
	if !ok || e.n != n {
		return appendBuf{}, false
	}
	// A live weak pointer points to the object it was made for, so a string
	// at its address is a view of that buffer.
	if e.b.Value() == nil && e.u.Value() == nil {
		delete(a.m, uintptr(p))
		return appendBuf{}, false
	}
	return e, true
}

func (a *appendBufs) add(p unsafe.Pointer, e appendBuf) {
	if a.m == nil {
		a.m = make(map[uintptr]appendBuf)
		a.sweepAt = 64
	}
	a.m[uintptr(p)] = e
	a.peak = max(a.peak, len(a.m))
	if len(a.m) >= a.sweepAt {
		a.sweep()
	}
}

// sweep drops the entries of buffers that are gone. A Go map does not
// shrink, so one left mostly empty is made again at its size.
func (a *appendBufs) sweep() {
	for k, e := range a.m {
		if e.b.Value() == nil && e.u.Value() == nil {
			delete(a.m, k)
		}
	}
	switch {
	case len(a.m) == 0:
		a.m, a.peak = nil, 0
	case len(a.m) < a.peak/4:
		m := make(map[uintptr]appendBuf, len(a.m))
		for k, e := range a.m {
			m[k] = e
		}
		a.m, a.peak = m, len(m)
	}
	a.sweepAt = max(2*len(a.m), 64)
}

// release sweeps the buffers and forgets the marks, between runs.
func (a *appendBufs) release() {
	a.sweep()
	a.marks = nil
}

// marked returns, and forgets, the steps of the chain that made the string
// at p of n code units (the BOM included), or 0 if it is not marked.
func (a *appendBufs) marked(p unsafe.Pointer, n int) int {
	if a.marks == nil {
		return 0
	}
	m := &a.marks[markSlot(p)]
	if m.p != uintptr(p) {
		return 0
	}
	m.p = 0
	if int(m.n) != n {
		return 0
	}
	return int(m.steps)
}

func (a *appendBufs) mark(p unsafe.Pointer, n, steps int) {
	if a.marks == nil {
		a.marks = new([appendMarkSlots]appendMark)
	}
	a.marks[markSlot(p)] = appendMark{p: uintptr(p), n: uint32(n), steps: uint32(min(steps, appendPromoteSteps))}
}

// markSlot is the slot of the table of marks for the address p.
func markSlot(p unsafe.Pointer) int {
	return int(uint64(uintptr(p)) * 0x9e3779b97f4a7c15 >> (64 - appendMarkBits))
}

// concatString is left + right for the + operator.
func (r *Runtime) concatString(left, right String) String {
	return r.concat(left, right, false)
}

// concatStringChained is left + right for a + whose left operand is the
// result of the + before it in the same expression: a later step of the
// same chain, which appends in place to a buffer but does not count as a
// step of its own, nor promote its left operand to a buffer.
func (r *Runtime) concatStringChained(left, right String) String {
	return r.concat(left, right, true)
}

func (r *Runtime) concat(left, right String, chained bool) String {
	ll, rl := left.Length(), right.Length()
	if rl > maxStringLength-ll {
		r.throwInvalidStringLength()
	}
	if ll < appendMinLength || rl == 0 {
		// The result is a new string of both lengths.
		r.countAlloc(int64(ll + rl))
		return left.Concat(right)
	}
	return r.appendStringsChain(left, []String{right}, ll+rl, chained)
}

// appendStrings is first followed by rest, which is length code units long
// in all and at most maxStringLength. It charges what it allocates.
func (r *Runtime) appendStrings(first String, rest []String, length int) String {
	return r.appendStringsChain(first, rest, length, false)
}

// appendStringsChain is appendStrings, as a later step of first's chain if
// chained.
func (r *Runtime) appendStringsChain(first String, rest []String, length int, chained bool) String {
	fa, fu := devirtualizeString(first)
	ascii := fu == nil
	for i, s := range rest {
		switch s := s.(type) {
		case asciiString:
		case unicodeString:
			ascii = false
		default:
			a, u := devirtualizeString(s)
			if u != nil {
				ascii = false
				rest[i] = u
			} else {
				rest[i] = a
			}
		}
	}
	if ascii {
		return r.appendASCII(fa, rest, length, chained)
	}
	return r.appendUnicode(fa, fu, rest, length, chained)
}

// promotes reports whether a concatenation onto a string marked with the
// steps of its chain goes into a buffer with room.
func promotes(steps int, chained bool) bool {
	return !chained && steps >= appendPromoteSteps-1
}

// nextSteps is the steps of the chain a result's mark records.
func nextSteps(steps int, chained bool) int {
	if chained {
		return steps
	}
	return steps + 1
}

func (r *Runtime) appendASCII(first asciiString, rest []String, length int, chained bool) String {
	var buf []byte
	var w weak.Pointer[byte]
	var p0 unsafe.Pointer // the buffer written in place
	grown, steps := false, 0
	if p := unsafe.Pointer(unsafe.StringData(string(first))); p != nil {
		if e, ok := r.appendBufs.lookup(p, len(first)); ok && e.u == (weak.Pointer[uint16]{}) {
			grown = true
			if length <= e.c {
				buf, w, p0 = unsafe.Slice((*byte)(p), e.c)[:len(first)], e.b, p
			} else {
				delete(r.appendBufs.m, uintptr(p))
				buf = r.newASCIIBuf(first, 2*length)
			}
		} else if steps = r.appendBufs.marked(p, len(first)); promotes(steps, chained) {
			// A string being built: leave it room, a quarter at first, so
			// that a long expression made once holds little more than
			// its result.
			grown = true
			buf = r.newASCIIBuf(first, promotedRoom(length))
		}
	}
	if buf == nil {
		// Made at its exact size, and charged as the + operator charges
		// its results.
		r.countAlloc(int64(length))
		buf = make([]byte, len(first), length)
		copy(buf, first)
	}
	for _, s := range rest {
		buf = append(buf, s.(asciiString)...)
	}
	p := unsafe.Pointer(unsafe.SliceData(buf))
	if !grown {
		r.appendBufs.mark(p, len(buf), nextSteps(steps, chained))
	} else {
		if p0 != nil && p != p0 {
			// The room was not enough (it always is): append moved the
			// string, so the weak pointer is not the buffer's.
			delete(r.appendBufs.m, uintptr(p0))
			w = weak.Pointer[byte]{}
		}
		if w == (weak.Pointer[byte]{}) {
			w = weak.Make((*byte)(p))
		}
		r.appendBufs.add(p, appendBuf{b: w, n: len(buf), c: cap(buf)})
	}
	return asciiString(unsafe.String((*byte)(p), len(buf)))
}

// promotedRoom is the capacity of the first buffer of a string of length
// code units being built. A buffer that fills is replaced by one twice the
// length.
func promotedRoom(length int) int {
	return length + max(length/4, 64)
}

// newASCIIBuf returns a buffer, charged, for a string of up to c bytes that
// starts with first.
func (r *Runtime) newASCIIBuf(first asciiString, c int) []byte {
	c = min(c, maxStringLength)
	r.countAlloc(int64(c))
	buf := make([]byte, len(first), c)
	copy(buf, first)
	return buf
}

func (r *Runtime) appendUnicode(fa asciiString, fu unicodeString, rest []String, length int, chained bool) String {
	var buf []uint16
	var w weak.Pointer[uint16]
	var p0 unsafe.Pointer // the buffer written in place
	grown, steps := false, 0
	if fu != nil {
		p := unsafe.Pointer(unsafe.SliceData(fu))
		if e, ok := r.appendBufs.lookup(p, len(fu)); ok && e.b == (weak.Pointer[byte]{}) {
			grown = true
			if length+1 <= e.c {
				buf, w, p0 = unsafe.Slice((*uint16)(p), e.c)[:len(fu)], e.u, p
			} else {
				delete(r.appendBufs.m, uintptr(p))
				buf = r.newUnicodeBuf(fa, fu, 2*length)
			}
		} else if steps = r.appendBufs.marked(p, len(fu)); promotes(steps, chained) {
			grown = true
			buf = r.newUnicodeBuf(fa, fu, promotedRoom(length))
		}
	} else if p := unsafe.Pointer(unsafe.StringData(string(fa))); p != nil {
		if _, ok := r.appendBufs.lookup(p, len(fa)); ok {
			// A string being built in bytes meets its first unit past
			// ASCII: it goes on in a buffer of units, with room.
			delete(r.appendBufs.m, uintptr(p))
			grown = true
		} else if steps = r.appendBufs.marked(p, len(fa)); promotes(steps, chained) {
			grown = true
		}
		if grown {
			buf = r.newUnicodeBuf(fa, nil, promotedRoom(length))
		}
	}
	if buf == nil {
		// Made at its exact size, and charged as the + operator charges
		// its results, in code units.
		r.countAlloc(int64(length))
		buf = unicodeBufOf(fa, fu, length+1)
	}
	for _, s := range rest {
		switch s := s.(type) {
		case asciiString:
			buf = appendASCIIUnits(buf, s)
		case unicodeString:
			buf = append(buf, s[1:]...)
		}
	}
	p := unsafe.Pointer(unsafe.SliceData(buf))
	if !grown {
		r.appendBufs.mark(p, len(buf), nextSteps(steps, chained))
	} else {
		if p0 != nil && p != p0 {
			// The room was not enough (it always is): append moved the
			// string, so the weak pointer is not the buffer's.
			delete(r.appendBufs.m, uintptr(p0))
			w = weak.Pointer[uint16]{}
		}
		if w == (weak.Pointer[uint16]{}) {
			w = weak.Make((*uint16)(p))
		}
		r.appendBufs.add(p, appendBuf{u: w, n: len(buf), c: cap(buf)})
	}
	// The capacity is cut to the length, so that nothing appending to the
	// result writes into the room left for the buffer's next string.
	return unicodeString(buf[:len(buf):len(buf)])
}

// newUnicodeBuf returns a buffer of units, charged, for a string of up to
// c code units that starts with the BOM and the units of fa or fu.
func (r *Runtime) newUnicodeBuf(fa asciiString, fu unicodeString, c int) []uint16 {
	c = min(c, maxStringLength) + 1
	r.countAlloc(2 * int64(c))
	return unicodeBufOf(fa, fu, c)
}

// unicodeBufOf returns a slice of units of capacity c that holds the BOM
// and the units of fa or fu.
func unicodeBufOf(fa asciiString, fu unicodeString, c int) []uint16 {
	buf := make([]uint16, 0, c)
	if fu != nil {
		return append(buf, fu...)
	}
	return appendASCIIUnits(append(buf, unistring.BOM), fa)
}

// appendASCIIUnits appends the bytes of s to buf as code units.
func appendASCIIUnits(buf []uint16, s asciiString) []uint16 {
	n := len(buf)
	buf = slices.Grow(buf, len(s))[:n+len(s)]
	dst := buf[n:]
	for i := range dst {
		dst[i] = uint16(s[i])
	}
	return buf
}
