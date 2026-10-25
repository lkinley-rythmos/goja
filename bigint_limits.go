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
// longer of them). The digits of the result are then charged to the
// allocation counter (SetAllocationLimit), also before the result is made.
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
	r.countAlloc(int64(n) * (bigIntDigitBits / 8))
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
		return new(big.Int)
	}
	r.chargeBigInt(bigDigits(x) - bigDigits(y) + 1)
	return new(big.Int).Quo(x, y)
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
	return new(big.Int).Rem(x, y)
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
	return new(big.Int).Not(x)
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
	return new(big.Int).And(x, y)
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
	return new(big.Int).Or(x, y)
}

func (r *Runtime) bigXor(x, y *big.Int) *big.Int {
	n := max(bigDigits(x), bigDigits(y))
	if (x.Sign() < 0) != (y.Sign() < 0) {
		n++
	}
	r.chargeBigInt(n)
	return new(big.Int).Xor(x, y)
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
			return big.NewInt(-1)
		}
		return new(big.Int)
	}
	r.chargeBigInt(bitsToDigits(int64(x.BitLen()) - int64(n) + 1))
	return new(big.Int).Rsh(x, n)
}

// bigExp is x ** y, which throws where V8's BigInt::Exponentiate does.
func (r *Runtime) bigExp(x, y *big.Int) *big.Int {
	if y.Sign() < 0 {
		panic(r.newError(r.getRangeError(), "Exponent must be positive"))
	}
	if y.Sign() == 0 {
		return big.NewInt(1)
	}
	if x.Sign() == 0 {
		return x
	}
	if x.CmpAbs(big.NewInt(1)) == 0 {
		// (-1n) ** y is 1n for an even y.
		if x.Sign() < 0 && y.Bit(0) == 0 {
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
		r.countAlloc(int64(2*bigDigits(z)) * (bigIntDigitBits / 8))
		z = r.mulSigned(z, z)
		if n>>i&1 != 0 {
			r.countAlloc(int64(bigDigits(z)+bigDigits(x)) * (bigIntDigitBits / 8))
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

// bigAsUintN is BigInt.asUintN(n, x).
func (r *Runtime) bigAsUintN(n int64, x *big.Int) *big.Int {
	if x.Sign() == 0 {
		return x
	}
	if n == 0 {
		return new(big.Int)
	}
	if x.Sign() < 0 {
		// 2^n - (|x| mod 2^n), which V8 makes n bits long.
		if n > bigIntMaxLengthBits {
			r.throwBigIntTooBig()
		}
		t := bigLowBits(x, uint(n))
		if t.Sign() == 0 {
			return t
		}
		r.chargeBigInt(bitsToDigits(n))
		m := new(big.Int).Lsh(big.NewInt(1), uint(n))
		return m.Sub(m, t)
	}
	if int64(x.BitLen()) <= n {
		return x
	}
	r.chargeBigInt(bitsToDigits(n))
	return bigLowBits(x, uint(n))
}

// bigAsIntN is BigInt.asIntN(n, x).
func (r *Runtime) bigAsIntN(n int64, x *big.Int) *big.Int {
	if n == 0 {
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
	// Here n <= bits(x), so neither result is longer than x.
	r.chargeBigInt(bitsToDigits(n))
	z := bigLowBits(x, uint(n))
	if x.Sign() < 0 {
		// The low n bits of x in two's complement.
		if z.Sign() != 0 {
			m := new(big.Int).Lsh(big.NewInt(1), uint(n))
			z.Sub(m, z)
		}
	}
	if z.Bit(int(n-1)) != 0 {
		z.Sub(z, new(big.Int).Lsh(big.NewInt(1), uint(n)))
	}
	return z
}

// bigLowBits returns |x| mod 2^n, the low n bits of |x|.
func bigLowBits(x *big.Int, n uint) *big.Int {
	if n >= uint(x.BitLen()) {
		return new(big.Int).Abs(x)
	}
	k := (int(n) + bits.UintSize - 1) / bits.UintSize
	z := make([]big.Word, k)
	copy(z, x.Bits())
	if b := n % bits.UintSize; b != 0 {
		z[k-1] &= 1<<b - 1
	}
	return new(big.Int).SetBits(z)
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
// interrupted.
func (r *Runtime) mulAbs(x, y []big.Word) *big.Int {
	if len(x) < len(y) {
		x, y = y, x
	}
	if len(y) == 0 {
		return new(big.Int)
	}
	square := len(x) == len(y) && &x[0] == &y[0]
	if len(x)*len(y) <= bigMulLeafProduct {
		a := new(big.Int).SetBits(x)
		if square {
			return new(big.Int).Mul(a, a)
		}
		return new(big.Int).Mul(a, new(big.Int).SetBits(y))
	}
	r.vm.checkInterrupt()
	h := (len(x) + 1) / 2
	shift := uint(h * bits.UintSize)
	x0, x1 := natNorm(x[:h]), x[h:]
	if len(y) <= h {
		// x1 * y * B^h + x0 * y
		z := r.mulAbs(x1, y)
		z.Lsh(z, shift)
		return z.Add(z, r.mulAbs(x0, y))
	}
	y0, y1 := x0, x1
	if !square {
		y0, y1 = natNorm(y[:h]), y[h:]
	}
	z2 := r.mulAbs(x1, y1)
	z0 := r.mulAbs(x0, y0)
	// z1 = (x0 + x1) * (y0 + y1) - z2 - z0
	sx := new(big.Int).Add(new(big.Int).SetBits(x0), new(big.Int).SetBits(x1))
	sy := sx
	if !square {
		sy = new(big.Int).Add(new(big.Int).SetBits(y0), new(big.Int).SetBits(y1))
	}
	z1 := r.mulAbs(sx.Bits(), sy.Bits())
	z1.Sub(z1, z2)
	z1.Sub(z1, z0)
	z2.Lsh(z2, 2*shift)
	z1.Lsh(z1, shift)
	z2.Add(z2, z1)
	return z2.Add(z2, z0)
}

// natNorm drops the high zero words of x.
func natNorm(x []big.Word) []big.Word {
	i := len(x)
	for i > 0 && x[i-1] == 0 {
		i--
	}
	return x[:i]
}
