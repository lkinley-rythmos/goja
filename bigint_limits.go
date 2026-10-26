package goja

import (
	"math"
	"math/big"
	"math/bits"
)

// BigInt operations are limited as V8 10.2 (Node.js 18) limits them on a
// 64-bit platform: a BigInt has at most BigInt::kMaxLength 64-bit digits
// (kMaxLengthBits, 2^30 bits), and an operation whose result needs more
// throws "RangeError: Maximum BigInt size exceeded". Each operation decides
// it before it makes its result, from the lengths of its operands, by the
// rule V8 uses for that operation, which for some is an upper bound (x + y
// with x and y of the same sign is taken to need one digit more than the
// longer of them). The result is then charged to the allocation counter
// (SetAllocationLimit), its digits and its header (allocSizeBigInt), also
// before it is made.
const (
	bigIntMaxLengthBits = 1 << 30
	bigIntDigitBits     = 64
	bigIntMaxLength     = bigIntMaxLengthBits / bigIntDigitBits
)

var bigIntTwo = big.NewInt(2)

// bigDigits is the number of V8 digits x has.
func bigDigits(x *big.Int) int {
	return bitsToDigits(int64(x.BitLen()))
}

// bitsToDigits is the number of V8 digits n bits take, given as an int64 so
// that sums of bit lengths do not overflow on 32-bit platforms.
func bitsToDigits(n int64) int {
	return int(min((n+bigIntDigitBits-1)/bigIntDigitBits, bigIntMaxLength+1))
}

//go:noinline
func (r *Runtime) throwBigIntTooBig() {
	panic(r.newError(r.getRangeError(), "Maximum BigInt size exceeded"))
}

// chargeBigInt is called before a BigInt result of n digits is made: it
// refuses one longer than V8's limit, and charges its digits to the
// allocation counter.
func (r *Runtime) chargeBigInt(n int) {
	if n > bigIntMaxLength {
		r.throwBigIntTooBig()
	}
	r.chargeBigIntDigits(int64(n))
}

// chargeBigIntDigits charges a BigInt of n digits, as other values are
// charged, however small: its digits and its header.
func (r *Runtime) chargeBigIntDigits(n int64) {
	r.countAlloc(n*(bigIntDigitBits/8) + allocSizeBigInt)
}

// chargeBigIntMagnitudeUp is chargeBigInt for a result whose magnitude is
// one more than x's (x + 1n for x >= 0n, x - 1n for x <= 0n, ~x for
// x >= 0n), which V8 measures exactly: it has one digit more only when all
// the bits of |x| are set and fill its last digit.
func (r *Runtime) chargeBigIntMagnitudeUp(x *big.Int) {
	n := bigDigits(x)
	if bl := x.BitLen(); bl%bigIntDigitBits == 0 && bigBitsAllSet(x) {
		n = bitsToDigits(int64(bl) + 1)
	}
	r.chargeBigInt(n)
}

func bigBitsAllSet(x *big.Int) bool {
	for _, w := range x.Bits() {
		if w != ^big.Word(0) {
			return false
		}
	}
	return true
}

func (r *Runtime) bigAdd(x, y *big.Int) *big.Int {
	if x.Sign() == 0 {
		return y
	}
	if y.Sign() == 0 {
		return x
	}
	n := max(bigDigits(x), bigDigits(y))
	if x.Sign() == y.Sign() {
		n++
	}
	r.chargeBigInt(n)
	return new(big.Int).Add(x, y)
}

func (r *Runtime) bigSub(x, y *big.Int) *big.Int {
	if y.Sign() == 0 {
		return x
	}
	if x.Sign() == 0 {
		r.chargeBigInt(bigDigits(y))
		return new(big.Int).Neg(y)
	}
	n := max(bigDigits(x), bigDigits(y))
	if x.Sign() != y.Sign() {
		n++
	}
	r.chargeBigInt(n)
	return new(big.Int).Sub(x, y)
}

func (r *Runtime) bigMul(x, y *big.Int) *big.Int {
	if x.Sign() == 0 {
		return x
	}
	if y.Sign() == 0 {
		return y
	}
	r.chargeBigInt(bigDigits(x) + bigDigits(y))
	return r.mulSigned(x, y)
}

// mulSigned is x * y, by mulAbs.
func (r *Runtime) mulSigned(x, y *big.Int) *big.Int {
	z := r.mulAbs(x.Bits(), y.Bits())
	if x.Sign() != y.Sign() {
		z.Neg(z)
	}
	return z
}

func (r *Runtime) bigQuo(x, y *big.Int) *big.Int {
	if y.Sign() == 0 {
		panic(r.newError(r.getRangeError(), "Division by zero"))
	}
	if x.CmpAbs(y) < 0 {
		r.chargeBigInt(0)
		return new(big.Int)
	}
	r.chargeBigInt(bigDigits(x) - bigDigits(y) + 1)
	q, _ := r.divAbs(x, y)
	if x.Sign() != y.Sign() {
		q.Neg(q)
	}
	return q
}

// bigRem is x % y: the remainder has the sign of the dividend.
func (r *Runtime) bigRem(x, y *big.Int) *big.Int {
	if y.Sign() == 0 {
		panic(r.newError(r.getRangeError(), "Division by zero"))
	}
	if x.CmpAbs(y) < 0 {
		return x
	}
	r.chargeBigInt(bigDigits(y))
	m := r.remAbs(x, y)
	if x.Sign() < 0 {
		m.Neg(m)
	}
	return m
}

func (r *Runtime) bigNeg(x *big.Int) *big.Int {
	if x.Sign() == 0 {
		return x
	}
	r.chargeBigInt(bigDigits(x))
	return new(big.Int).Neg(x)
}

func (r *Runtime) bigInc(x *big.Int) *big.Int {
	if x.Sign() >= 0 {
		r.chargeBigIntMagnitudeUp(x)
	} else {
		r.chargeBigInt(bigDigits(x))
	}
	return new(big.Int).Add(x, big.NewInt(1))
}

func (r *Runtime) bigDec(x *big.Int) *big.Int {
	if x.Sign() <= 0 {
		r.chargeBigIntMagnitudeUp(x)
	} else {
		r.chargeBigInt(bigDigits(x))
	}
	return new(big.Int).Sub(x, big.NewInt(1))
}

func (r *Runtime) bigNot(x *big.Int) *big.Int {
	if x.Sign() >= 0 {
		r.chargeBigIntMagnitudeUp(x)
	} else {
		r.chargeBigInt(bigDigits(x))
	}
	if len(x.Bits()) <= bigBitwiseLeafWords {
		return new(big.Int).Not(x)
	}
	// ~x = -(x + 1) and ~-m = m - 1, a block at a time.
	if x.Sign() < 0 {
		return new(big.Int).SetBits(r.natDec(x.Bits()))
	}
	z := r.natInc(r.natCopy(x.Bits()))
	return new(big.Int).Neg(new(big.Int).SetBits(z))
}

// natCopy is a copy of x, made a block at a time.
func (r *Runtime) natCopy(x []big.Word) []big.Word {
	z := make([]big.Word, len(x), len(x)+1)
	for i := 0; i < len(x); i += bigBlockWords {
		r.vm.checkInterrupt()
		copy(z[i:], x[i:min(i+bigBlockWords, len(x))])
	}
	return z
}

// The lengths V8 gives the results of the bitwise operators, which work on
// two's complement: for operands of opposite signs (zero counts as
// positive), x & y is as long as the positive one, x | y as the negative
// one and x ^ y one digit longer than the longer one; for two negative
// operands, x & y is one digit longer than the longer one, x | y as long as
// the shorter one and x ^ y as the longer one.

func (r *Runtime) bigAnd(x, y *big.Int) *big.Int {
	lx, ly := bigDigits(x), bigDigits(y)
	switch nx, ny := x.Sign() < 0, y.Sign() < 0; {
	case !nx && !ny:
		r.chargeBigInt(min(lx, ly))
	case nx && ny:
		r.chargeBigInt(max(lx, ly) + 1)
	case nx:
		r.chargeBigInt(ly)
	default:
		r.chargeBigInt(lx)
	}
	return r.bigBitwise(x, y, '&')
}

func (r *Runtime) bigOr(x, y *big.Int) *big.Int {
	lx, ly := bigDigits(x), bigDigits(y)
	switch nx, ny := x.Sign() < 0, y.Sign() < 0; {
	case !nx && !ny:
		r.chargeBigInt(max(lx, ly))
	case nx && ny:
		r.chargeBigInt(min(lx, ly))
	case nx:
		r.chargeBigInt(lx)
	default:
		r.chargeBigInt(ly)
	}
	return r.bigBitwise(x, y, '|')
}

func (r *Runtime) bigXor(x, y *big.Int) *big.Int {
	n := max(bigDigits(x), bigDigits(y))
	if (x.Sign() < 0) != (y.Sign() < 0) {
		n++
	}
	r.chargeBigInt(n)
	return r.bigBitwise(x, y, '^')
}

// bigShift is x << y, or x >> y when right is set: V8's BigInt::LeftShift
// and SignedRightShift.
func (r *Runtime) bigShift(x, y *big.Int, right bool) *big.Int {
	if y.Sign() == 0 || x.Sign() == 0 {
		return x
	}
	if (y.Sign() < 0) != right {
		return r.bigShiftRightAbs(x, y)
	}
	return r.bigShiftLeftAbs(x, y)
}

// bigShiftAmount returns |y| if it is at most V8's limit in bits.
func bigShiftAmount(y *big.Int) (uint, bool) {
	if y.BitLen() > 31 {
		return 0, false
	}
	n := uint(new(big.Int).Abs(y).Uint64())
	return n, n <= bigIntMaxLengthBits
}

func (r *Runtime) bigShiftLeftAbs(x, y *big.Int) *big.Int {
	n, ok := bigShiftAmount(y)
	if !ok {
		r.throwBigIntTooBig()
	}
	r.chargeBigInt(bitsToDigits(int64(x.BitLen()) + int64(n)))
	return new(big.Int).Lsh(x, n)
}

func (r *Runtime) bigShiftRightAbs(x, y *big.Int) *big.Int {
	n, ok := bigShiftAmount(y)
	if !ok || n >= uint(x.BitLen()) {
		// What V8 calls RightShiftByMaximum.
		if x.Sign() < 0 {
			r.chargeBigInt(1)
			return big.NewInt(-1)
		}
		r.chargeBigInt(0)
		return new(big.Int)
	}
	r.chargeBigInt(bitsToDigits(int64(x.BitLen()) - int64(n) + 1))
	// A long x a block at a time, and a negative one as -ceil(|x| / 2^n),
	// which big.Int.Rsh makes from a copy of |x| - 1: 128 MB, uncharged and
	// in one step, for a result of one digit.
	var z []big.Word
	if len(x.Bits()) > bigBlockWords {
		z = r.natShr(x.Bits(), n)
	} else {
		z = new(big.Int).Rsh(new(big.Int).SetBits(x.Bits()), n).Bits()
	}
	if x.Sign() >= 0 {
		return new(big.Int).SetBits(z)
	}
	if x.TrailingZeroBits() < n {
		z = r.natInc(z)
	}
	return new(big.Int).Neg(new(big.Int).SetBits(z))
}

// bigExp is x ** y, which throws where V8's BigInt::Exponentiate does.
func (r *Runtime) bigExp(x, y *big.Int) *big.Int {
	if y.Sign() < 0 {
		panic(r.newError(r.getRangeError(), "Exponent must be positive"))
	}
	if y.Sign() == 0 {
		r.chargeBigInt(1)
		return big.NewInt(1)
	}
	if x.Sign() == 0 {
		return x
	}
	if x.CmpAbs(big.NewInt(1)) == 0 {
		// (-1n) ** y is 1n for an even y.
		if x.Sign() < 0 && y.Bit(0) == 0 {
			r.chargeBigInt(1)
			return big.NewInt(1)
		}
		return x
	}
	if y.BitLen() > bigIntDigitBits {
		r.throwBigIntTooBig()
	}
	e := y.Uint64()
	if e == 1 {
		return x
	}
	if e >= bigIntMaxLengthBits {
		r.throwBigIntTooBig()
	}
	n := int(e)
	if x.CmpAbs(bigIntTwo) == 0 {
		r.chargeBigInt(1 + n/bigIntDigitBits)
		z := new(big.Int).Lsh(big.NewInt(1), uint(n))
		if x.Sign() < 0 && n&1 != 0 {
			z.Neg(z)
		}
		return z
	}
	switch bigExpLengths(x, n) {
	case bigExpThrows:
		r.throwBigIntTooBig()
	case bigExpUndecided:
		return r.bigExpV8(x, n)
	}
	// V8 does not throw, so the powers are made the faster way, from the
	// highest bit of n down, multiplying by x, which is short, rather than
	// by the square before.
	z := x
	for i := bits.Len(uint(n)) - 2; i >= 0; i-- {
		r.chargeBigIntDigits(2 * int64(bigDigits(z)))
		z = r.mulSigned(z, z)
		if n>>i&1 != 0 {
			r.chargeBigIntDigits(int64(bigDigits(z)) + int64(bigDigits(x)))
			z = r.mulSigned(z, x)
		}
	}
	return z
}

// bigExpV8 is |x| ** n made as V8 makes it, from the lowest bit of n up,
// each product refused as V8 refuses it.
func (r *Runtime) bigExpV8(x *big.Int, n int) *big.Int {
	var z *big.Int
	sq := x
	if n&1 != 0 {
		z = x
	}
	for n >>= 1; n != 0; n >>= 1 {
		sq = r.bigMul(sq, sq)
		if n&1 != 0 {
			if z == nil {
				z = sq
			} else {
				z = r.bigMul(z, sq)
			}
		}
	}
	return z
}

const (
	bigExpFits = iota
	bigExpThrows
	bigExpUndecided
)

// bigExpLengths tells whether one of the products of V8's loop for
// |x| ** n (bigExpV8; n > 1, |x| > 2) is longer than V8's limit, from the
// lengths of the powers of x, which are known within a bit from log2|x|.
// When those cannot tell, the products are checked as they are made.
// Without it, a power past the limit was computed for minutes before it
// threw: 4n ** (2n ** 29n - 1n) takes V8 26 s.
func bigExpLengths(x *big.Int, n int) int {
	bl := x.BitLen()
	// log2|x| = scale + log2(top), with top the leading 64 bits of |x|. A
	// power of two is exact; otherwise the error of k * log2|x| is far
	// below one bit, for any k up to 2^30.
	abs := new(big.Int).SetBits(x.Bits())
	scale := max(bl-64, 0)
	log2Top := math.Log2(float64(new(big.Int).Rsh(abs, uint(scale)).Uint64()))
	margin := 1.0
	if x.TrailingZeroBits() == uint(bl-1) {
		scale, log2Top, margin = bl-1, 0, 0
	}
	// digits bounds the digits of |x| ** k, whose bits are
	// floor(k * log2|x|) + 1, k * scale exactly.
	digits := func(k int, m float64) int {
		return bitsToDigits(int64(k)*int64(scale) + int64(math.Floor(float64(k)*log2Top+m)) + 1)
	}
	result := bigExpFits
	product := func(k1, k2 int) bool {
		if digits(k1, -margin)+digits(k2, -margin) > bigIntMaxLength {
			result = bigExpThrows
			return true
		}
		if digits(k1, margin)+digits(k2, margin) > bigIntMaxLength {
			result = bigExpUndecided
		}
		return false
	}
	sq, z := 1, 0
	if n&1 != 0 {
		z = 1
	}
	for n >>= 1; n != 0; n >>= 1 {
		if product(sq, sq) {
			break
		}
		sq *= 2
		if n&1 != 0 {
			if z == 0 {
				z = sq
			} else {
				if product(z, sq) {
					break
				}
				z += sq
			}
		}
	}
	return result
}

// bigAsUintN is BigInt.asUintN(n, x). The result is made a block of words
// at a time, with the interrupt checked between them (bigLowWords,
// bigNegModPow2), and charged as long as it is made, before.
func (r *Runtime) bigAsUintN(n int64, x *big.Int) *big.Int {
	if x.Sign() == 0 {
		return x
	}
	if n == 0 {
		r.chargeBigInt(0)
		return new(big.Int)
	}
	if x.Sign() < 0 {
		// 2^n - (|x| mod 2^n), which V8 makes n bits long.
		if n > bigIntMaxLengthBits {
			r.throwBigIntTooBig()
		}
		if x.TrailingZeroBits() >= uint(n) {
			// |x| mod 2^n is 0.
			r.chargeBigInt(0)
			return new(big.Int)
		}
		r.chargeBigInt(bitsToDigits(n))
		w := r.bigLowWords(x, uint(n))
		r.bigNegModPow2(w, uint(n))
		return new(big.Int).SetBits(natNorm(w))
	}
	if int64(x.BitLen()) <= n {
		return x
	}
	r.chargeBigInt(bitsToDigits(n))
	return new(big.Int).SetBits(natNorm(r.bigLowWords(x, uint(n))))
}

// bigAsIntN is BigInt.asIntN(n, x), made and charged as bigAsUintN.
func (r *Runtime) bigAsIntN(n int64, x *big.Int) *big.Int {
	if n == 0 {
		r.chargeBigInt(0)
		return new(big.Int)
	}
	if x.Sign() == 0 {
		return x
	}
	// x is returned when -2^(n-1) <= x < 2^(n-1).
	bl := int64(x.BitLen())
	if bl < n || bl == n && x.Sign() < 0 && x.TrailingZeroBits() == uint(n-1) {
		return x
	}
	// Here n <= bits(x), so the result is no longer than x.
	r.chargeBigInt(bitsToDigits(n))
	w := r.bigLowWords(x, uint(n)) // t = |x| mod 2^n
	t := new(big.Int).SetBits(natNorm(w))
	if x.Sign() >= 0 {
		// t, or t - 2^n = -(2^n - t) when bit n - 1 is set.
		if t.Bit(int(n-1)) == 0 {
			return t
		}
		r.bigNegModPow2(w, uint(n))
		return new(big.Int).Neg(new(big.Int).SetBits(natNorm(w)))
	}
	// x mod 2^n is u = 2^n - t (t != 0), and the result u, or u - 2^n = -t
	// when bit n - 1 of u is set, that is when t <= 2^(n-1).
	if t.Sign() == 0 {
		return t
	}
	if tl := int64(t.BitLen()); tl < n || tl == n && t.TrailingZeroBits() == uint(n-1) {
		return t.Neg(t)
	}
	r.bigNegModPow2(w, uint(n))
	return new(big.Int).SetBits(natNorm(w))
}

// bigBlockWords is how many words the linear steps that check the
// interrupt between blocks handle in one (2^20: about 1 ms).
const bigBlockWords = 1 << 20

// bigLowWords returns the low n bits of |x| in words: as many as n bits
// take, copied a block at a time, with the interrupt checked between them.
func (r *Runtime) bigLowWords(x *big.Int, n uint) []big.Word {
	k := (int(n) + bits.UintSize - 1) / bits.UintSize
	w := make([]big.Word, k)
	src := x.Bits()
	src = src[:min(len(src), k)]
	for i := 0; i < len(src); i += bigBlockWords {
		r.vm.checkInterrupt()
		copy(w[i:], src[i:min(i+bigBlockWords, len(src))])
	}
	if b := n % bits.UintSize; b != 0 {
		w[k-1] &= 1<<b - 1
	}
	return w
}

// bigNegModPow2 sets w, the low n bits of a number t != 0, to 2^n - t:
// t's complement in n bits, plus one, a block at a time.
func (r *Runtime) bigNegModPow2(w []big.Word, n uint) {
	for i := 0; i < len(w); i += bigBlockWords {
		r.vm.checkInterrupt()
		for j := i; j < min(i+bigBlockWords, len(w)); j++ {
			w[j] = ^w[j]
		}
	}
	if b := n % bits.UintSize; b != 0 {
		w[len(w)-1] &= 1<<b - 1
	}
	for i := range w {
		w[i]++
		if w[i] != 0 {
			break
		}
	}
}

// bigMulLeafProduct bounds the products mulAbs leaves to big.Int.Mul, which
// cannot be interrupted, by the product of the operands' lengths in words:
// two operands of 2^13 words take it about 8 ms on a recent laptop, one of
// 2^24 words and one of 4 about as long.
const bigMulLeafProduct = 1 << 26

// mulAbs returns |x| * |y|, given their words. A large product is split into
// smaller ones by Karatsuba's method, as big.Int.Mul splits it, but with the
// pending interrupt checked between them: two operands of 2^22 words take
// big.Int.Mul more than two minutes, during which the runtime could not be
// interrupted. The parts are put together a block at a time (natAddAt), and
// a long operand by a short one is multiplied a block of the long one at a
// time (mulBlocks): one word by 2^24 took big.Int.Mul 20 ms, and putting the
// parts of a product near V8's limit together took it as long a step.
func (r *Runtime) mulAbs(x, y []big.Word) *big.Int {
	if len(x) < len(y) {
		x, y = y, x
	}
	if len(y) == 0 {
		return new(big.Int)
	}
	square := len(x) == len(y) && &x[0] == &y[0]
	if mulIsLeaf(len(x), len(y)) && len(x) <= bigBlockWords {
		a := new(big.Int).SetBits(x)
		if square {
			return new(big.Int).Mul(a, a)
		}
		return new(big.Int).Mul(a, new(big.Int).SetBits(y))
	}
	r.vm.checkInterrupt()
	h := (len(x) + 1) / 2
	if len(y) <= h {
		return r.mulBlocks(x, y)
	}
	x0, x1 := natNorm(x[:h]), x[h:]
	y0, y1 := x0, x1
	if !square {
		y0, y1 = natNorm(y[:h]), y[h:]
	}
	z2 := r.mulAbs(x1, y1)
	z0 := r.mulAbs(x0, y0)
	// z1 = (x0 + x1) * (y0 + y1) - z2 - z0
	sx := r.natAdd(x0, x1)
	sy := sx
	if !square {
		sy = r.natAdd(y0, y1)
	}
	z1 := r.mulAbs(sx, sy).Bits()
	z1 = r.natSubInPlace(z1, z2.Bits())
	z1 = r.natSubInPlace(z1, z0.Bits())
	// z2 * B^2h + z1 * B^h + z0, z0 < B^2h.
	z := make([]big.Word, len(x)+len(y)+1)
	r.natCopyAt(z, z0.Bits(), 0)
	r.natCopyAt(z, z2.Bits(), 2*h)
	r.natAddAt(z, z1, h)
	return new(big.Int).SetBits(natNorm(z))
}

// mulBlocks is mulAbs of x by y no longer than half of x: x is multiplied
// a block at a time, each block as long as y or long enough for a step of
// big.Int.Mul, and the products are added in place.
func (r *Runtime) mulBlocks(x, y []big.Word) *big.Int {
	s := max(len(y), min(bigBlockWords, bigMulLeafProduct/len(y)))
	z := make([]big.Word, len(x)+len(y))
	for i := 0; i < len(x); i += s {
		r.vm.checkInterrupt()
		p := r.mulAbs(natNorm(x[i:min(i+s, len(x))]), y)
		r.natAddAt(z, p.Bits(), i)
	}
	return new(big.Int).SetBits(natNorm(z))
}

// mulIsLeaf reports whether operands of m and n words make a product
// mulAbs leaves to big.Int.Mul: m * n <= bigMulLeafProduct, written so that
// m * n cannot overflow an int on a 32-bit platform.
func mulIsLeaf(m, n int) bool {
	return m == 0 || n <= bigMulLeafProduct/m
}

// natNorm drops the high zero words of x.
func natNorm(x []big.Word) []big.Word {
	i := len(x)
	for i > 0 && x[i-1] == 0 {
		i--
	}
	return x[:i]
}

// bigBitwiseLeafWords is the length up to which a bitwise operation is left
// to big.Int; past it, the operation is made a block of words at a time
// (bigBitwise): big.Int.Or of two negative numbers of 2^24 words took
// 0.13 s.
const bigBitwiseLeafWords = 1 << 16

// bigBitwise is x & y, x | y or x ^ y (op), in two's complement, as
// big.Int computes them, a block of words at a time, with the interrupt
// checked between them. A negative number -m is ~(m - 1) in two's
// complement.
func (r *Runtime) bigBitwise(x, y *big.Int, op byte) *big.Int {
	if max(len(x.Bits()), len(y.Bits())) <= bigBitwiseLeafWords {
		// big.Int copies a negative operand (|x| - 1), as below.
		if x.Sign() < 0 {
			r.chargeWords(len(x.Bits()))
		}
		if y.Sign() < 0 {
			r.chargeWords(len(y.Bits()))
		}
		switch op {
		case '&':
			return new(big.Int).And(x, y)
		case '|':
			return new(big.Int).Or(x, y)
		}
		return new(big.Int).Xor(x, y)
	}
	if x.Sign() < 0 && y.Sign() >= 0 {
		x, y = y, x
	}
	xa, ya := x.Bits(), y.Bits()
	nx, ny := x.Sign() < 0, y.Sign() < 0
	var z []big.Word
	neg := false
	switch {
	case !nx && !ny:
		z = r.natBitwise(xa, ya, op)
	case nx && ny:
		// |x| - 1 and |y| - 1 are copies, charged: 1n & nx, nx | -1n made
		// 128 MB for a result of one digit.
		r.chargeWords(len(xa) + len(ya))
		x1, y1 := r.natDec(xa), r.natDec(ya)
		switch op {
		case '&':
			// -(((|x|-1) | (|y|-1)) + 1)
			z, neg = r.natInc(r.natBitwise(x1, y1, '|')), true
		case '|':
			// -(((|x|-1) & (|y|-1)) + 1)
			z, neg = r.natInc(r.natBitwise(x1, y1, '&')), true
		default:
			// (|x|-1) ^ (|y|-1)
			z = r.natBitwise(x1, y1, '^')
		}
	default:
		// x >= 0, y < 0.
		r.chargeWords(len(ya))
		y1 := r.natDec(ya)
		switch op {
		case '&':
			// x &^ (|y|-1)
			z = r.natBitwise(xa, y1, 'n')
		case '|':
			// -(((|y|-1) &^ x) + 1)
			z, neg = r.natInc(r.natBitwise(y1, xa, 'n')), true
		default:
			// -((x ^ (|y|-1)) + 1)
			z, neg = r.natInc(r.natBitwise(xa, y1, '^')), true
		}
	}
	res := new(big.Int).SetBits(natNorm(z))
	if neg {
		res.Neg(res)
	}
	return res
}

// natBitwise is x op y for magnitudes: & | ^, or 'n' for x &^ y.
func (r *Runtime) natBitwise(x, y []big.Word, op byte) []big.Word {
	n := max(len(x), len(y))
	if op == '&' {
		n = min(len(x), len(y))
	} else if op == 'n' {
		n = len(x)
	}
	z := make([]big.Word, n)
	for i := 0; i < n; i += bigBlockWords {
		r.vm.checkInterrupt()
		for j := i; j < min(i+bigBlockWords, n); j++ {
			var a, b big.Word
			if j < len(x) {
				a = x[j]
			}
			if j < len(y) {
				b = y[j]
			}
			switch op {
			case '&':
				z[j] = a & b
			case '|':
				z[j] = a | b
			case '^':
				z[j] = a ^ b
			default:
				z[j] = a &^ b
			}
		}
	}
	return z
}

// natDec is x - 1 for a magnitude x > 0, copied a block at a time.
func (r *Runtime) natDec(x []big.Word) []big.Word {
	z := r.natCopy(x)
	for i := range z {
		z[i]--
		if z[i] != ^big.Word(0) {
			break
		}
	}
	return natNorm(z)
}

// natInc is z + 1 for a magnitude z, in place when there is room.
func (r *Runtime) natInc(z []big.Word) []big.Word {
	for i := range z {
		z[i]++
		if z[i] != 0 {
			return z
		}
	}
	return append(z, 1)
}
