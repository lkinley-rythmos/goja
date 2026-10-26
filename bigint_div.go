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
// words by one of n in about 10 ms at most. Below 128 words, big.Int
// divides by schoolbook, about 15 ns per word of the divisor and word of
// the quotient; above, recursively, at about (n + 1024) ns per word of the
// quotient (a quotient of 131,072 words by 128 took 118 ms), and slowly
// whenever the divisor is long (1.9 s for one word by 2^24).
func divIsLeaf(qw, n int) bool {
	if n < 128 {
		return qw <= (1<<19)/(n+16)
	}
	return n <= 1<<13 && qw <= (1<<23)/(n+1024)
}

// recipLeafWords is the length up to which recipAbs divides directly
// (B^(2n) / b of 2^12 words takes big.Int about 5 ms).
const recipLeafWords = 1 << 12

// divAbs returns |x| / |y| and |x| % |y|, y non-zero.
func (r *Runtime) divAbs(x, y *big.Int) (q, m *big.Int) {
	return r.divAbsQ(x, y, true)
}

// remAbs is |x| % |y|, y non-zero, without the quotient where it is made a
// block at a time (it took 128 MB, zeroed in one step, for a remainder of a
// number of 2^24 words).
func (r *Runtime) remAbs(x, y *big.Int) *big.Int {
	_, m := r.divAbsQ(x, y, false)
	return m
}

// divAbsQ is divAbs, its quotient left nil when wantQ is not set and the
// division is made a block at a time.
func (r *Runtime) divAbsQ(x, y *big.Int, wantQ bool) (q, m *big.Int) {
	a := new(big.Int).SetBits(x.Bits())
	b := new(big.Int).SetBits(y.Bits())
	if a.Cmp(b) < 0 {
		return new(big.Int), a
	}
	n := len(b.Bits())
	qw := len(a.Bits()) - n + 1
	if divIsLeaf(qw, n) {
		// QuoRem makes both results, the one the caller does not charge
		// included, and a copy of a for the remainder.
		r.chargeWords(len(a.Bits()) + 2)
		return new(big.Int).QuoRem(a, b, new(big.Int))
	}
	r.vm.checkInterrupt()
	if qw+1 < n {
		return r.divShortQuotient(a, b, qw)
	}
	if k := divLeafQuotientWords(n); k > 0 {
		return r.divChunked(a, b, k, wantQ)
	}
	return r.divLongQuotient(a, b, wantQ)
}

// divLeafQuotientWords is how many words of quotient a division by n words
// makes in one step of big.Int.QuoRem (divIsLeaf), or 0 if n is too long
// for a step.
func divLeafQuotientWords(n int) int {
	switch {
	case n < 128:
		return (1<<19)/(n+16) - 1
	case n <= 1<<13:
		return (1<<23)/(n+1024) - 1
	}
	return 0
}

// divChunked divides a by b a block of k words of the quotient at a time,
// each block one step of big.Int.QuoRem: dividing a long number by a short
// one block by block from the divisor's reciprocal took 4.7 s for 2^24
// words by one.
func (r *Runtime) divChunked(a, b *big.Int, k int, wantQ bool) (q, m *big.Int) {
	aw := a.Bits()
	blocks := (len(aw) + k - 1) / k
	var qw []big.Word
	if wantQ {
		qw = make([]big.Word, blocks*k)
	}
	m = new(big.Int)
	shift := uint(k * bits.UintSize)
	for i := blocks - 1; i >= 0; i-- {
		r.vm.checkInterrupt()
		// t, its quotient and remainder, and QuoRem's copy of t.
		r.chargeWords(3*(k+len(b.Bits())) + 3)
		t := new(big.Int).Lsh(m, shift)
		t.Add(t, new(big.Int).SetBits(natNorm(aw[i*k:min((i+1)*k, len(aw))])))
		qi := new(big.Int)
		qi, m = qi.QuoRem(t, b, new(big.Int))
		if wantQ {
			copy(qw[i*k:(i+1)*k], qi.Bits())
		}
	}
	if !wantQ {
		return nil, m
	}
	return new(big.Int).SetBits(natNorm(qw)), m
}

// divShortQuotient divides a by b, whose quotient has at most qw words, qw
// + 1 fewer than b has: the leading qw + 1 words of b and the words of a
// above the same place give the quotient within a few units (the words left
// out change a / b by less than 3), which the remainder corrects. The
// product and the remainder, as long as b, are charged before they are
// made: x / y of two numbers near V8's limit made 256 MB of them for a
// quotient of one word.
func (r *Runtime) divShortQuotient(a, b *big.Int, qw int) (q, m *big.Int) {
	aw, bw := a.Bits(), b.Bits()
	sh := len(bw) - qw - 1
	// Shifted by whole words: the leading words themselves.
	q, _ = r.divAbs(new(big.Int).SetBits(aw[sh:]), new(big.Int).SetBits(bw[sh:]))
	n := len(q.Bits()) + len(bw)
	r.chargeWords(n + max(n, len(aw)))
	m = r.bigSubAbs(aw, r.mulAbs(q.Bits(), bw).Bits())
	return q, r.divCorrect(q, m, b)
}

// divCorrect adjusts q and m = a - q * b until 0 <= m < b, and returns m,
// whose words, made by the division, it may change in place. Each step
// that makes a new number charges it.
func (r *Runtime) divCorrect(q, m, b *big.Int) *big.Int {
	one := big.NewInt(1)
	bw := b.Bits()
	for m.Sign() < 0 {
		q.Sub(q, one)
		if mw := m.Bits(); r.natCmp(mw, bw) <= 0 {
			// m + b = b - |m|
			if cap(mw) < len(bw) {
				r.chargeWords(len(bw))
			}
			m = new(big.Int).SetBits(r.natRevSubInPlace(mw, bw))
		} else {
			m = new(big.Int).Neg(new(big.Int).SetBits(r.natSubInPlace(mw, bw)))
		}
	}
	for r.natCmp(m.Bits(), bw) >= 0 {
		q.Add(q, one)
		m = new(big.Int).SetBits(r.natSubInPlace(m.Bits(), bw))
	}
	return m
}

// divLongQuotient divides a by b, whose quotient has at least as many words
// as b less one: normalized so that b's top bit is set, a is divided a block
// of n words (b's length) at a time, each block of the quotient
// floor(t * inv / B^(2n)) for t the remainder so far followed by the block
// and inv = floor(B^(2n) / b), which is at most 2 less than t / b. The
// numbers it makes on the way, each as long as b or longer, are charged
// before they are made.
func (r *Runtime) divLongQuotient(a, b *big.Int, wantQ bool) (q, m *big.Int) {
	s := uint(bits.LeadingZeros(uint(b.Bits()[len(b.Bits())-1])))
	if s != 0 {
		// The shifted operands, and the remainder shifted back.
		r.chargeWords(len(a.Bits()) + 2*len(b.Bits()) + 2)
		a = new(big.Int).SetBits(r.natShl(a.Bits(), s))
		b = new(big.Int).SetBits(r.natShl(b.Bits(), s))
	}
	bw := b.Bits()
	n := len(bw)
	inv := r.recipAbs(b)
	aw := a.Bits()
	blocks := (len(aw) + n - 1) / n
	var qw []big.Word
	if wantQ {
		qw = make([]big.Word, blocks*n)
	}
	var mw []big.Word
	for i := blocks - 1; i >= 0; i-- {
		r.vm.checkInterrupt()
		// t = m * B^n + the block; then t * inv, qi * b, and t - qi * b.
		tn := n + len(mw)
		r.chargeWords(tn + (tn + len(inv.Bits())) + 2*n + 1 + tn)
		t := make([]big.Word, tn)
		r.natCopyAt(t, aw[i*n:min((i+1)*n, len(aw))], 0)
		r.natCopyAt(t, mw, n)
		t = natNorm(t)
		// qi = t * inv / B^(2n), its words past the 2n lowest.
		qi := r.mulAbs(t, inv.Bits())
		if qb := qi.Bits(); len(qb) > 2*n {
			qi.SetBits(qb[2*n:])
		} else {
			qi.SetBits(nil)
		}
		m = r.bigSubAbs(t, r.mulAbs(qi.Bits(), bw).Bits())
		m = r.divCorrect(qi, m, b)
		mw = m.Bits()
		if wantQ {
			r.natCopyAt(qw, qi.Bits(), i*n)
		}
	}
	if wantQ {
		q = new(big.Int).SetBits(natNorm(qw))
	}
	m = new(big.Int).SetBits(mw)
	if s != 0 {
		m.SetBits(r.natShr(mw, s))
	}
	return q, m
}

// recipAbs returns floor(B^(2n) / b) for b of n words with its top bit set
// (B = 2^bits.UintSize), by Newton's method: the reciprocal of b's leading
// words, which is close enough that one step of
// x + x * (B^(2n) - b * x) / B^(2n) is within a few units, which are then
// corrected. The numbers it makes, from n to 3n words long, are charged
// before they are made.
func (r *Runtime) recipAbs(b *big.Int) *big.Int {
	n := len(b.Bits())
	if n <= recipLeafWords {
		pow := new(big.Int).Lsh(big.NewInt(1), uint(2*n*bits.UintSize))
		return pow.Quo(pow, b)
	}
	r.vm.checkInterrupt()
	// The leading h words, h - 2 more than half of n: the reciprocal they
	// give is within B^(n + 1 - h) of b's, and after the step within B^(n +
	// 2 - 2h) < 1/B, and a unit or two of rounding.
	h := (n+1)/2 + 2
	k := n - h
	x := r.recipAbs(new(big.Int).SetBits(b.Bits()[k:]))
	// x << k words, b * x, B^(2n) - b * x, x * e, x +- d, B^(2n) - b * x.
	r.chargeWords((len(x.Bits()) + k + 1) + 2*(2*n+2) + (3*n + 3) + (n + 2) + 2*(2*n+2))
	xs := make([]big.Word, len(x.Bits())+k)
	r.natCopyAt(xs, x.Bits(), k)
	x.SetBits(xs)
	e := r.powSub(2*n, r.mulAbs(b.Bits(), x.Bits()).Bits())
	d := r.mulAbs(x.Bits(), e.Bits())
	if dw := d.Bits(); len(dw) > 2*n {
		d.SetBits(dw[2*n:])
	} else {
		d.SetBits(nil)
	}
	if e.Sign() < 0 {
		x = r.bigSubAbs(x.Bits(), d.Bits())
	} else {
		x = new(big.Int).SetBits(r.natAdd(x.Bits(), d.Bits()))
	}
	m := r.powSub(2*n, r.mulAbs(b.Bits(), x.Bits()).Bits())
	r.divCorrect(x, m, b)
	return x
}
