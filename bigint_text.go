package goja

import (
	"math/big"
	"math/bits"
	"strings"
)

// Large BigInts are written in parts, as large products are (mulAbs), with
// the pending interrupt checked between them: big.Int.Text cannot be
// interrupted, and took 8.4 s to write a number of 31.7M bits in decimal,
// 85 s one of 2^21 words.

// bigTextLeafWords is the length up to which big.Int.Text writes a number
// itself (2^11 words in decimal: about 2 ms).
const bigTextLeafWords = 1 << 11

// bigText returns x in radix, as big.Int.Text does: a radix that is a
// power of two at once, any other by halves, dividing by powers of the
// radix (divAbs).
func (r *Runtime) bigText(x *big.Int, radix int) string {
	if len(x.Bits()) <= bigTextLeafWords {
		return x.Text(radix)
	}
	if radix&(radix-1) == 0 {
		return r.bigTextPow2(x, radix)
	}
	var sb strings.Builder
	if x.Sign() < 0 {
		sb.WriteByte('-')
	}
	// pows[i] is radix^(digits << i), digits as many as fit in the leaf.
	digits := bigTextLeafWords * bits.UintSize * 1000 / bigLog2Milli(radix) / 2
	pows := []*big.Int{new(big.Int).Exp(big.NewInt(int64(radix)), big.NewInt(int64(digits)), nil)}
	for 2*len(pows[len(pows)-1].Bits()) <= len(x.Bits()) {
		r.vm.checkInterrupt()
		p := pows[len(pows)-1]
		pows = append(pows, r.mulAbs(p.Bits(), p.Bits()))
	}
	r.writeText(&sb, new(big.Int).SetBits(x.Bits()), radix, pows, digits, -1)
	return sb.String()
}

// bigTextPow2 is big.Int.Text for a radix that is a power of two, a block
// of words at a time (a number of 2^24 words takes Text 0.3 s in hex): the
// blocks are a whole number of digits, 2^16 words, or 3 or 5 times 2^14 in
// base 8 or 32.
func (r *Runtime) bigTextPow2(x *big.Int, radix int) string {
	perDigit := bits.TrailingZeros(uint(radix))
	block := 1 << 16
	if bits.UintSize%perDigit != 0 {
		block = perDigit << 14
	}
	digits := block * bits.UintSize / perDigit
	w := x.Bits()
	var sb strings.Builder
	if x.Sign() < 0 {
		sb.WriteByte('-')
	}
	top := (len(w) - 1) / block
	for i := top; i >= 0; i-- {
		r.vm.checkInterrupt()
		s := new(big.Int).SetBits(natNorm(w[i*block : min((i+1)*block, len(w))])).Text(radix)
		if i < top && len(s) < digits {
			sb.WriteString(strings.Repeat("0", digits-len(s)))
		}
		sb.WriteString(s)
	}
	return sb.String()
}

// writeText writes x, padded with zeros to width digits when width >= 0.
func (r *Runtime) writeText(sb *strings.Builder, x *big.Int, radix int, pows []*big.Int, digits, width int) {
	if len(x.Bits()) <= bigTextLeafWords {
		s := x.Text(radix)
		if width > len(s) {
			sb.WriteString(strings.Repeat("0", width-len(s)))
		}
		sb.WriteString(s)
		return
	}
	r.vm.checkInterrupt()
	// The power about half as long as x.
	k := len(pows) - 1
	for k > 0 && 2*len(pows[k].Bits()) > len(x.Bits())+1 {
		k--
	}
	q, m := r.divAbs(x, pows[k])
	low := digits << k
	if width >= 0 {
		width -= low
	}
	r.writeText(sb, q, radix, pows, digits, width)
	r.writeText(sb, m, radix, pows, digits, low)
}

// bigLog2Milli is 1000 * log2(radix), rounded up.
func bigLog2Milli(radix int) int {
	return []int{0, 0, 1000, 1585, 2000, 2322, 2585, 2808, 3000, 3170, 3322, 3460, 3585, 3701, 3808, 3907,
		4000, 4088, 4170, 4248, 4322, 4393, 4460, 4524, 4585, 4644, 4701, 4755, 4808, 4858, 4907, 4955,
		5000, 5045, 5088, 5130, 5170}[radix]
}

// bigMaxBitsPerChar is V8's kMaxBitsPerChar: 32 * log2(radix), rounded up.
var bigMaxBitsPerChar = [...]int64{0, 0, 32, 51, 64, 75, 83, 90, 96, 102, 107, 111, 115, 119, 122, 126, 128,
	131, 134, 136, 139, 141, 143, 145, 147, 149, 151, 153, 154, 156, 158, 159, 160, 162, 163, 165, 166}

// bigTextLengthV8 is how long V8 takes the string of x in radix to be
// (ToStringResultLength), which it refuses past String::kMaxLength: for a
// radix that is a power of two exactly, its sign counted twice; for any
// other at most, counting each character as only (bits per character - 1/32)
// bits.
func bigTextLengthV8(x *big.Int, radix int) int64 {
	n := int64(x.BitLen())
	var sign int64
	if x.Sign() < 0 {
		sign = 1
	}
	if radix&(radix-1) == 0 {
		perChar := int64(bits.TrailingZeros(uint(radix)))
		return (n+perChar-1)/perChar + 2*sign
	}
	min := bigMaxBitsPerChar[radix] - 1
	return (n*32+min-1)/min + sign
}

// bigString is x in radix as a string: refused as V8 refuses it, charged to
// the allocation counter before it is made, and made in parts (bigText).
func (r *Runtime) bigString(x *big.Int, radix int) String {
	n := bigTextLengthV8(x, radix)
	if n > maxStringLength {
		r.throwInvalidStringLength()
	}
	r.countAlloc(n)
	return asciiString(r.bigText(x, radix))
}

// toStringOf is v.toString(), with a BigInt written by bigString.
func (r *Runtime) toStringOf(v Value) String {
	if b, ok := v.(*valueBigInt); ok {
		return r.bigIntString(b)
	}
	return v.toString()
}

// bigIntString is b in decimal by bigString.
func (r *Runtime) bigIntString(b *valueBigInt) String {
	return r.bigString((*big.Int)(b), 10)
}
