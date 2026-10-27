package goja

import (
	"fmt"
	"math/big"
	"math/bits"
	"strings"

	"github.com/dop251/goja/unistring"
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
// radix (divAbs). n is at least the length of the text, which the caller
// charges: the text is made in a buffer of that size. The parts, the
// powers and the quotients, and the text big.Int.Text makes of each part
// (bigTextLeaf) are charged here before they are made: they were 2 MB
// uncharged for x of 2^20 bits.
func (r *Runtime) bigText(x *big.Int, radix int, n int64) string {
	if len(x.Bits()) <= bigTextLeafWords {
		r.chargeTextLeaf(len(x.Bits()), radix)
		return x.Text(radix)
	}
	// The caller charges n; Go makes the buffer in its size class, or
	// whole pages.
	r.countAlloc(allocSize(n) - n)
	var sb strings.Builder
	sb.Grow(int(n))
	if radix&(radix-1) == 0 {
		r.bigTextPow2(&sb, x, radix)
		return sb.String()
	}
	if x.Sign() < 0 {
		sb.WriteByte('-')
	}
	// pows[i] is radix^(digits << i), digits as many as fit in the leaf.
	digits := bigTextLeafWords * bits.UintSize * 1000 / bigLog2Milli(radix) / 2
	// radix^digits and the powers big.Int.Exp makes on the way, eight
	// buffers of up to half a leaf.
	r.chargeBuffers(bigTextLeafWords/2+1, bigTextLeafWords/2+1, bigTextLeafWords/2+1, bigTextLeafWords/2+1,
		bigTextLeafWords/2+1, bigTextLeafWords/2+1, bigTextLeafWords/2+1, bigTextLeafWords/2+1)
	// radix^digits, the two numbers it is made of, and x's copy.
	r.chargeHeaders(4)
	pows := []*big.Int{new(big.Int).Exp(big.NewInt(int64(radix)), big.NewInt(int64(digits)), nil)}
	for 2*len(pows[len(pows)-1].Bits()) <= len(x.Bits()) {
		r.vm.checkInterrupt()
		p := pows[len(pows)-1]
		r.chargeWords(2*len(p.Bits()) + 1)
		pows = append(pows, r.mulAbs(p.Bits(), p.Bits()))
	}
	r.writeText(&sb, new(big.Int).SetBits(x.Bits()), radix, pows, digits, -1)
	return sb.String()
}

// chargeTextLeaf charges what big.Int.Text makes for a number of n words
// in radix, which was measured (go1.27.3, every radix 2 to 36, 1 to 2048
// words, math/big's pooled scratch empty and full) and is charged with a
// margin: the text, as bytes and as a string, each in its size class
// (allocSize); in a radix that is a power of two nothing else, and in
// any other up to 8 words nothing more than 152 bytes, but past 8 words
// the copy it divides, the powers of the radix it divides by and the
// quotients and remainders of its divisions, up to 28.2 words a word
// (1500 words in radix 33 with the pool empty, as a collection leaves it;
// 13 with it full). It charged 3 words a word, and nothing below 64
// words: twenty x.toString(36) of 2048 words made 4.5 MB and were charged
// 2.5 MB.
func (r *Runtime) chargeTextLeaf(n, radix int) {
	text := 2 * allocSize(int64(n)*bits.UintSize*1000/int64(bigLog2Milli(radix))+2)
	switch {
	case radix&(radix-1) == 0:
		r.countAlloc(text + 64)
	case n <= 8:
		r.countAlloc(text + 64 + 16*int64(n))
	default:
		r.countAlloc(text + 32*int64(n)*(bits.UintSize/8) + 1024)
	}
}

// writeZeros writes n zeros.
func writeZeros(sb *strings.Builder, n int) {
	const zeros = "0000000000000000000000000000000000000000000000000000000000000000"
	for ; n > len(zeros); n -= len(zeros) {
		sb.WriteString(zeros)
	}
	sb.WriteString(zeros[:max(n, 0)])
}

// bigTextPow2 is big.Int.Text for a radix that is a power of two, a block
// of words at a time (a number of 2^24 words takes Text 0.3 s in hex): the
// blocks are a whole number of digits, 2^16 words, or 3 or 5 times 2^14 in
// base 8 or 32.
func (r *Runtime) bigTextPow2(sb *strings.Builder, x *big.Int, radix int) {
	perDigit := bits.TrailingZeros(uint(radix))
	block := 1 << 16
	if bits.UintSize%perDigit != 0 {
		block = perDigit << 14
	}
	digits := block * bits.UintSize / perDigit
	w := x.Bits()
	if x.Sign() < 0 {
		sb.WriteByte('-')
	}
	top := (len(w) - 1) / block
	for i := top; i >= 0; i-- {
		r.vm.checkInterrupt()
		b := natNorm(w[i*block : min((i+1)*block, len(w))])
		// The digits as bytes and as a string, each in whole pages (a
		// number of 2049 words in hex made 24 KB a call more than it was
		// charged).
		r.countAlloc(2 * allocSize(int64(len(b)*bits.UintSize/perDigit+2)))
		r.chargeHeaders(1)
		s := new(big.Int).SetBits(b).Text(radix)
		if i < top {
			writeZeros(sb, digits-len(s))
		}
		sb.WriteString(s)
	}
}

// writeText writes x, padded with zeros to width digits when width >= 0.
func (r *Runtime) writeText(sb *strings.Builder, x *big.Int, radix int, pows []*big.Int, digits, width int) {
	if len(x.Bits()) <= bigTextLeafWords {
		r.chargeTextLeaf(len(x.Bits()), radix)
		s := x.Text(radix)
		writeZeros(sb, width-len(s))
		sb.WriteString(s)
		return
	}
	r.vm.checkInterrupt()
	// The power about half as long as x.
	k := len(pows) - 1
	for k > 0 && 2*len(pows[k].Bits()) > len(x.Bits())+1 {
		k--
	}
	// The quotient (divAbs charges the remainder).
	r.chargeWords(len(x.Bits()) - len(pows[k].Bits()) + 2)
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
	r.startBigOp()
	r.countAlloc(n)
	return asciiString(r.bigText(x, radix, n))
}

// toStringOf is v.toString(), with a BigInt written by bigString.
func (r *Runtime) toStringOf(v Value) String {
	if b, ok := v.(*valueBigInt); ok {
		return r.bigIntString(b)
	}
	return v.toString()
}

// valueText is v.String(), as the messages and names that take a value's
// text in Go take it, but a BigInt written by bigString and a string
// converted by hostText: charged, and a block at a time.
func (r *Runtime) valueText(v Value) string {
	if v == nil {
		// As fmt writes a nil Value.
		return "<nil>"
	}
	switch s := r.valueString(v).(type) {
	case nil:
		return v.String()
	default:
		return r.hostText(s)
	}
}

// valueString is the text valueText takes of v, as a String: a string, a
// BigInt by bigString, a symbol's description, an object's ToString (which
// may run its toString), or nil for any other value, whose String is
// short.
func (r *Runtime) valueString(v Value) String {
	switch v := v.(type) {
	case String:
		return v
	case *valueBigInt:
		return r.bigIntString(v)
	case *Symbol:
		if v.desc == nil {
			return stringEmpty
		}
		return v.desc
	case *Object:
		p := v.toPrimitiveString()
		if s := r.valueString(p); s != nil {
			return s
		}
		return p.toString()
	}
	return nil
}

// valueTextString is valueText as a String, for a message joined by
// joinText.
func (r *Runtime) valueTextString(v Value) String {
	if v == nil {
		return asciiString("<nil>")
	}
	if s := r.valueString(v); s != nil {
		return s
	}
	return v.toString()
}

// keyText is a property key given as a fmt.Stringer, as a String.
func (r *Runtime) keyText(name fmt.Stringer) String {
	switch n := name.(type) {
	case unistring.String:
		return stringValueFromRaw(n)
	case Value:
		return r.valueTextString(n)
	}
	return newStringValue(name.String())
}

// textError is an error of type typ whose message is parts, joined by
// joinText, as a message that holds a script's value is.
func (r *Runtime) textError(typ *Object, parts ...String) *Object {
	return r.builtin_new(typ, []Value{r.joinText(parts...)})
}

// bigIntString is b in decimal by bigString.
func (r *Runtime) bigIntString(b *valueBigInt) String {
	return r.bigString((*big.Int)(b), 10)
}
