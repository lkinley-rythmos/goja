package goja

import (
	"math/big"
	"math/bits"
)

// Large BigInt divisions are made in parts, as large products are
// (mulAbs), with the pending interrupt checked between them: big.Int.QuoRem
// cannot be interrupted, and takes 30 s to divide a number of 2^21 words by
// one of 2^20, and 1.9 s even to divide a number of 2^24 words by one of
// almost as many.
//
// A division is left to big.Int.QuoRem when it is small (divIsLeaf). A
// longer one with a short quotient divides the leading words of its
// operands, which give the quotient within a few units, and corrects it
// from the remainder, a product made by mulAbs. One with a long quotient
// divides by the divisor's reciprocal, made by Newton's method from
// products (recipAbs), a block of the quotient as long as the divisor at a
// time.

// divIsLeaf reports whether big.Int.QuoRem divides a number of qw + n - 1
// words by one of n in about 10 ms at most. Below 100 words, big.Int
// divides by schoolbook, about 15 ns per word of the divisor and word of
// the quotient; above, recursively, about 5 ms for two numbers of 2^13 and
// 2^12 words, but slowly when the quotient is short and the divisor long
// (1.9 s for one word by 2^24).
func divIsLeaf(qw, n int) bool {
	if n < 128 {
		return qw <= (1<<19)/(n+16)
	}
	return n <= 1<<13 && qw <= (1<<24)/n
}

// recipLeafWords is the length up to which recipAbs divides directly
// (B^(2n) / b of 2^12 words takes big.Int about 5 ms).
const recipLeafWords = 1 << 12

// divAbs returns |x| / |y| and |x| % |y|, y non-zero.
func (r *Runtime) divAbs(x, y *big.Int) (q, m *big.Int) {
	a := new(big.Int).SetBits(x.Bits())
	b := new(big.Int).SetBits(y.Bits())
	if a.Cmp(b) < 0 {
		return new(big.Int), a
	}
	n := len(b.Bits())
	qw := len(a.Bits()) - n + 1
	if divIsLeaf(qw, n) {
		return new(big.Int).QuoRem(a, b, new(big.Int))
	}
	r.vm.checkInterrupt()
	if qw+1 < n {
		return r.divShortQuotient(a, b, qw)
	}
	if k := divLeafQuotientWords(n); k > 0 {
		return r.divChunked(a, b, k)
	}
	return r.divLongQuotient(a, b)
}

// divLeafQuotientWords is how many words of quotient a division by n words
// makes in one step of big.Int.QuoRem (divIsLeaf), or 0 if n is too long
// for a step.
func divLeafQuotientWords(n int) int {
	switch {
	case n < 128:
		return (1<<19)/(n+16) - 1
	case n <= 1<<13:
		return (1<<24)/n - 1
	}
	return 0
}

// divChunked divides a by b a block of k words of the quotient at a time,
// each block one step of big.Int.QuoRem: dividing a long number by a short
// one block by block from the divisor's reciprocal took 4.7 s for 2^24
// words by one.
func (r *Runtime) divChunked(a, b *big.Int, k int) (q, m *big.Int) {
	aw := a.Bits()
	blocks := (len(aw) + k - 1) / k
	qw := make([]big.Word, blocks*k)
	m = new(big.Int)
	shift := uint(k * bits.UintSize)
	for i := blocks - 1; i >= 0; i-- {
		r.vm.checkInterrupt()
		t := new(big.Int).Lsh(m, shift)
		t.Add(t, new(big.Int).SetBits(natNorm(aw[i*k:min((i+1)*k, len(aw))])))
		qi := new(big.Int)
		qi, m = qi.QuoRem(t, b, new(big.Int))
		copy(qw[i*k:(i+1)*k], qi.Bits())
	}
	return new(big.Int).SetBits(natNorm(qw)), m
}

// divShortQuotient divides a by b, whose quotient has at most qw words, qw
// + 1 fewer than b has: the leading qw + 1 words of b and the words of a
// above the same place give the quotient within a few units (the words left
// out change a / b by less than 3), which the remainder corrects.
func (r *Runtime) divShortQuotient(a, b *big.Int, qw int) (q, m *big.Int) {
	shift := uint(len(b.Bits())-qw-1) * bits.UintSize
	q, _ = r.divAbs(new(big.Int).Rsh(a, shift), new(big.Int).Rsh(b, shift))
	m = new(big.Int).Sub(a, r.mulAbs(q.Bits(), b.Bits()))
	return q, r.divCorrect(q, m, b)
}

// divCorrect adjusts q and m = a - q * b until 0 <= m < b, and returns m.
func (r *Runtime) divCorrect(q, m, b *big.Int) *big.Int {
	one := big.NewInt(1)
	for m.Sign() < 0 {
		q.Sub(q, one)
		m.Add(m, b)
	}
	for m.Cmp(b) >= 0 {
		q.Add(q, one)
		m.Sub(m, b)
	}
	return m
}

// divLongQuotient divides a by b, whose quotient has at least as many words
// as b less one: normalized so that b's top bit is set, a is divided a block
// of n words (b's length) at a time, each block of the quotient
// floor(t * inv / B^(2n)) for t the remainder so far followed by the block
// and inv = floor(B^(2n) / b), which is at most 2 less than t / b.
func (r *Runtime) divLongQuotient(a, b *big.Int) (q, m *big.Int) {
	s := uint(bits.LeadingZeros(uint(b.Bits()[len(b.Bits())-1])))
	if s != 0 {
		a = new(big.Int).Lsh(a, s)
		b = new(big.Int).Lsh(b, s)
	}
	n := len(b.Bits())
	inv := r.recipAbs(b)
	aw := a.Bits()
	blocks := (len(aw) + n - 1) / n
	qw := make([]big.Word, blocks*n)
	m = new(big.Int)
	blockShift := uint(n * bits.UintSize)
	for i := blocks - 1; i >= 0; i-- {
		r.vm.checkInterrupt()
		hi := min((i+1)*n, len(aw))
		t := new(big.Int).Lsh(m, blockShift)
		t.Add(t, new(big.Int).SetBits(natNorm(aw[i*n:hi])))
		qi := r.mulAbs(t.Bits(), inv.Bits())
		qi.Rsh(qi, 2*blockShift)
		m = t.Sub(t, r.mulAbs(qi.Bits(), b.Bits()))
		m = r.divCorrect(qi, m, b)
		copy(qw[i*n:(i+1)*n], qi.Bits())
	}
	q = new(big.Int).SetBits(natNorm(qw))
	if s != 0 {
		m.Rsh(m, s)
	}
	return q, m
}

// recipAbs returns floor(B^(2n) / b) for b of n words with its top bit set
// (B = 2^bits.UintSize), by Newton's method: the reciprocal of b's leading
// words, which is close enough that one step of
// x + x * (B^(2n) - b * x) / B^(2n) is within a few units, which are then
// corrected.
func (r *Runtime) recipAbs(b *big.Int) *big.Int {
	n := len(b.Bits())
	pow := new(big.Int).Lsh(big.NewInt(1), uint(2*n*bits.UintSize))
	if n <= recipLeafWords {
		return pow.Quo(pow, b)
	}
	r.vm.checkInterrupt()
	// The leading h words, h - 2 more than half of n: the reciprocal they
	// give is within B^(n + 1 - h) of b's, and after the step within B^(n +
	// 2 - 2h) < 1/B, and a unit or two of rounding.
	h := (n+1)/2 + 2
	k := uint((n - h) * bits.UintSize)
	x := r.recipAbs(new(big.Int).Rsh(b, k))
	x.Lsh(x, k)
	e := new(big.Int).Sub(pow, r.mulAbs(b.Bits(), x.Bits()))
	d := r.mulAbs(x.Bits(), e.Bits())
	d.Rsh(d, uint(2*n*bits.UintSize))
	if e.Sign() < 0 {
		x.Sub(x, d)
	} else {
		x.Add(x, d)
	}
	m := pow.Sub(pow, r.mulAbs(b.Bits(), x.Bits()))
	r.divCorrect(x, m, b)
	return x
}
