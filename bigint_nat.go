package goja

import (
	"math/big"
	"math/bits"
)

// Linear steps on the words of large BigInts (magnitudes, least significant
// word first, normalized: no high zero words), made a block of bigBlockWords
// words at a time with the interrupt checked between the blocks. big.Int's
// own are one step each: a subtraction of two numbers near V8's limit took
// up to 70 ms, its memory fresh, and the products, divisions and their
// corrections that use these ran several such steps in a row unchecked
// (x / y took 76 ms to return after an interrupt).

// chargeWords charges a temporary of n words, made by a BigInt operation
// whose result is shorter: the product and remainder of a division with a
// short quotient, the copy of a negative operand of a bitwise operation.
// It is called before the temporary is made, and charges what Go makes it
// in: its size class or pages, with the 4 words math/big makes past what
// it asks for (nat.make; the copy of a negative operand of 509 words of
// x & -y is made in 4864 bytes, and was charged 4072).
func (r *Runtime) chargeWords(n int) {
	r.countAlloc(allocSize(int64(n+4) * (bits.UintSize / 8)))
}

// math/big makes the scratch of its own products and divisions from a pool
// (getStack), which it grows as an operation needs, and which a garbage
// collection empties (a loop of x.toString() for x of 2^20 bits grew it
// by 1.2 MB a call). With the pool empty, a product whose shorter operand
// has n words made up to 14.4n words of it, a division by a number of n
// words up to 25n (both measured over the shapes mulAbs and divAbs leave
// to big.Int). A BigInt operation charges it before a part that may need
// more than the operation has been charged for it so far (the parts share
// the pool), when that is more than bigScratchCharged words: a product
// whose shorter operand is below 256 words, or a division by fewer than
// 147, is not charged it.
const bigScratchCharged = 1 << 12

// chargeMulScratch charges math/big's scratch for products whose shorter
// operands have at most n words.
func (r *Runtime) chargeMulScratch(n int) {
	r.chargeBigScratch(16 * n)
}

// chargeDivScratch charges math/big's scratch for divisions by numbers of
// at most n words.
func (r *Runtime) chargeDivScratch(n int) {
	r.chargeBigScratch(28 * n)
}

// chargeBigScratch charges w words of math/big's scratch, for the part of
// a BigInt operation about to need them, when the operation has not been
// charged as much for it yet: the pool grows into a new slice, as long as
// the part needs (its old one is left behind).
func (r *Runtime) chargeBigScratch(w int) {
	if w > bigScratchCharged && w > r.alloc.bigScratch {
		r.chargeWords(w)
		r.alloc.bigScratch = w
	}
}

// startBigOp starts a BigInt operation, with no scratch charged for it.
func (r *Runtime) startBigOp() {
	r.alloc.bigScratch = 0
}

// negWords is -z for the words z, made in them: big.Int.Neg of another
// big.Int copies its words.
func negWords(z []big.Word) *big.Int {
	x := new(big.Int).SetBits(z)
	return x.Neg(x)
}

// natCmp compares x and y.
func (r *Runtime) natCmp(x, y []big.Word) int {
	if len(x) != len(y) {
		if len(x) < len(y) {
			return -1
		}
		return 1
	}
	for i := len(x) - 1; i >= 0; i-- {
		if (len(x)-1-i)%bigBlockWords == bigBlockWords-1 {
			r.vm.checkInterrupt()
		}
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// natCopyAt copies x into z at word off.
func (r *Runtime) natCopyAt(z, x []big.Word, off int) {
	for i := 0; i < len(x); i += bigBlockWords {
		r.vm.checkInterrupt()
		copy(z[off+i:], x[i:min(i+bigBlockWords, len(x))])
	}
}

// natAddAt adds x to z at word off (z[off:] += x), z long enough for the
// sum.
func (r *Runtime) natAddAt(z, x []big.Word, off int) {
	var c uint
	for i := 0; i < len(x); i += bigBlockWords {
		r.vm.checkInterrupt()
		for j := i; j < min(i+bigBlockWords, len(x)); j++ {
			s, cc := bits.Add(uint(z[off+j]), uint(x[j]), c)
			z[off+j], c = big.Word(s), cc
		}
	}
	for k := off + len(x); c != 0; k++ {
		if (k-off-len(x))%bigBlockWords == bigBlockWords-1 {
			r.vm.checkInterrupt()
		}
		z[k]++
		if z[k] != 0 {
			c = 0
		}
	}
}

// natSubAt subtracts x from z at word off (z[off:] -= x), z >= x << off.
func (r *Runtime) natSubAt(z, x []big.Word, off int) {
	var b uint
	for i := 0; i < len(x); i += bigBlockWords {
		r.vm.checkInterrupt()
		for j := i; j < min(i+bigBlockWords, len(x)); j++ {
			d, bb := bits.Sub(uint(z[off+j]), uint(x[j]), b)
			z[off+j], b = big.Word(d), bb
		}
	}
	for k := off + len(x); b != 0; k++ {
		if (k-off-len(x))%bigBlockWords == bigBlockWords-1 {
			r.vm.checkInterrupt()
		}
		z[k]--
		if z[k] != ^big.Word(0) {
			b = 0
		}
	}
}

// natAdd is x + y, made.
func (r *Runtime) natAdd(x, y []big.Word) []big.Word {
	if len(x) < len(y) {
		x, y = y, x
	}
	z := make([]big.Word, len(x)+1)
	r.natCopyAt(z, x, 0)
	r.natAddAt(z, y, 0)
	return natNorm(z)
}

// natAddTo sets z, at least one word longer than x and y, to x + y, and
// returns it normalized.
func (r *Runtime) natAddTo(z, x, y []big.Word) []big.Word {
	if len(x) < len(y) {
		x, y = y, x
	}
	r.natCopyAt(z, x, 0)
	r.natClear(z[len(x):])
	r.natAddAt(z, y, 0)
	return natNorm(z)
}

// natSubInPlace is x - y, x >= y, made in x.
func (r *Runtime) natSubInPlace(x, y []big.Word) []big.Word {
	r.natSubAt(x, y, 0)
	return natNorm(x)
}

// natRevSubInPlace is y - x, x <= y, made in x when it has room.
func (r *Runtime) natRevSubInPlace(x, y []big.Word) []big.Word {
	z := x
	if cap(z) < len(y) {
		z = make([]big.Word, len(y))
		r.natCopyAt(z, x, 0)
	} else {
		z = z[:len(y)]
		clear(z[len(x):])
	}
	var b uint
	for i := 0; i < len(y); i += bigBlockWords {
		r.vm.checkInterrupt()
		for j := i; j < min(i+bigBlockWords, len(y)); j++ {
			d, bb := bits.Sub(uint(y[j]), uint(z[j]), b)
			z[j], b = big.Word(d), bb
		}
	}
	return natNorm(z)
}

// bigSubAbs is |x| - |y| as a signed number, made.
func (r *Runtime) bigSubAbs(x, y []big.Word) *big.Int {
	if max(len(x), len(y)) <= bigBlockWords {
		return new(big.Int).Sub(new(big.Int).SetBits(x), new(big.Int).SetBits(y))
	}
	neg := false
	if r.natCmp(x, y) < 0 {
		x, y, neg = y, x, true
	}
	z := make([]big.Word, len(x))
	r.natCopyAt(z, x, 0)
	res := new(big.Int).SetBits(r.natSubInPlace(z, y))
	if neg {
		res.Neg(res)
	}
	return res
}

// natShl is x << s bits, made.
func (r *Runtime) natShl(x []big.Word, s uint) []big.Word {
	ws, bs := int(s/bits.UintSize), s%bits.UintSize
	z := make([]big.Word, len(x)+ws+1)
	if bs == 0 {
		r.natCopyAt(z, x, ws)
		return natNorm(z)
	}
	var prev big.Word
	for i := 0; i < len(x); i += bigBlockWords {
		r.vm.checkInterrupt()
		for j := i; j < min(i+bigBlockWords, len(x)); j++ {
			z[ws+j] = x[j]<<bs | prev>>(bits.UintSize-bs)
			prev = x[j]
		}
	}
	z[ws+len(x)] = prev >> (bits.UintSize - bs)
	return natNorm(z)
}

// natShr is x >> s bits, made.
func (r *Runtime) natShr(x []big.Word, s uint) []big.Word {
	if ws := s / bits.UintSize; ws > 0 {
		if int(ws) >= len(x) {
			return nil
		}
		x, s = x[ws:], s%bits.UintSize
	}
	if len(x) == 0 {
		return nil
	}
	// A word of room, for the carry of -ceil(x / 2^s).
	z := make([]big.Word, len(x), len(x)+1)
	if s == 0 {
		r.natCopyAt(z, x, 0)
		return z
	}
	for i := 0; i < len(x); i += bigBlockWords {
		r.vm.checkInterrupt()
		for j := i; j < min(i+bigBlockWords, len(x)); j++ {
			var next big.Word
			if j+1 < len(x) {
				next = x[j+1]
			}
			z[j] = x[j]>>s | next<<(bits.UintSize-s)
		}
	}
	return natNorm(z)
}

// powSub is B^k - x (B = 2^bits.UintSize), signed, made a block at a time:
// the number of k words all of whose bits are set, less x, plus one, or,
// when x >= B^k, -(x - B^k).
func (r *Runtime) powSub(k int, x []big.Word) *big.Int {
	if len(x) > k {
		z := make([]big.Word, len(x))
		r.natCopyAt(z, x, 0)
		r.natSubAt(z, []big.Word{1}, k)
		return negWords(natNorm(z))
	}
	if len(x) == 0 {
		z := make([]big.Word, k+1)
		z[k] = 1
		return new(big.Int).SetBits(z)
	}
	// x != 0: B^k - x = (B^k - 1 - x) + 1, which is below B^k.
	z := make([]big.Word, k)
	for i := 0; i < k; i += bigBlockWords {
		r.vm.checkInterrupt()
		for j := i; j < min(i+bigBlockWords, k); j++ {
			var w big.Word
			if j < len(x) {
				w = x[j]
			}
			z[j] = ^w
		}
	}
	r.natAddAt(z, []big.Word{1}, 0)
	return new(big.Int).SetBits(natNorm(z))
}
