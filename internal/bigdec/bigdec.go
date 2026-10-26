// Package bigdec converts long strings of digits to a big.Int, and tells
// whether V8 takes a string of digits to be too long for a BigInt.
package bigdec

import (
	"math/big"
	"math/bits"
)

// leafDigits is the length up to which a string is converted by
// big.Int.SetString, which takes time quadratic in the number of decimal
// digits (2M: about 6 s), but about 20 µs for 4096 of them.
const leafDigits = 1 << 12

// Parse returns the value of s, a string of decimal digits and nothing else,
// or false if it is not one. A long string is split in two, the halves
// converted, and the high one multiplied by 10 to the length of the low one
// (powers of 10^leafDigits, reused), so that the conversion costs a few
// times the largest of those products: 2M digits take about 0.7 s.
func Parse(s string) (*big.Int, bool) {
	return ParseWith(s, nil, nil)
}

// ParseWith is Parse, multiplying with mul (big.Int.Mul if nil) and calling
// check (if not nil) between the parts, so that a caller can interrupt it.
func ParseWith(s string, mul func(x, y *big.Int) *big.Int, check func()) (*big.Int, bool) {
	if mul == nil {
		mul = func(x, y *big.Int) *big.Int { return new(big.Int).Mul(x, y) }
	}
	if check == nil {
		check = func() {}
	}
	if s == "" {
		return nil, false
	}
	for i := 0; i < len(s); i++ {
		if i%checkBlock == 0 {
			check()
		}
		if s[i] < '0' || s[i] > '9' {
			return nil, false
		}
	}
	if len(s) <= leafDigits {
		return leaf(s), true
	}
	// pows[i] is 10^(leafDigits << i), up to one at least half as long as s.
	pows := []*big.Int{new(big.Int).Exp(big.NewInt(10), big.NewInt(leafDigits), nil)}
	for partShorter(len(pows), len(s)) {
		check()
		p := pows[len(pows)-1]
		pows = append(pows, mul(p, p))
	}
	return parse(s, pows, len(pows)-1, mul, check), true
}

// parse converts s, of at most leafDigits << (i + 1) digits.
func parse(s string, pows []*big.Int, i int, mul func(x, y *big.Int) *big.Int, check func()) *big.Int {
	if len(s) <= leafDigits {
		return leaf(s)
	}
	check()
	for !partShorter(i, len(s)) {
		i--
	}
	// The low part is leafDigits << i digits long, the high one at most as
	// long; leafDigits << i < len(s), so the shift does not overflow.
	k := len(s) - leafDigits<<i
	z := mul(parse(s[:k], pows, i, mul, check), pows[i])
	return z.Add(z, parse(s[k:], pows, i-1, mul, check))
}

// partShorter reports whether leafDigits << i < n, written so that the
// shift cannot overflow an int on a 32-bit platform (n can be up to 2^31 - 1
// there, and leafDigits << 19 is 2^31).
func partShorter(i, n int) bool {
	return n > 0 && leafDigits <= (n-1)>>i
}

// leaf converts s, made in words of the room it takes: big.Int.SetString
// grows them four at a time, making 40 KB for 4096 digits (1.8 KB so).
func leaf(s string) *big.Int {
	z := new(big.Int).SetBits(make([]big.Word, 0, (len(s)*3402/1000+bits.UintSize-1)/bits.UintSize+1))
	z, _ = z.SetString(s, 10)
	return z
}

// V8 parses the digits of a BigInt (a string or a literal) into parts of as
// many digits as fit in a 64-bit word, and refuses one of more than
// BigInt::kMaxLength (2^24) parts, leading zeros left out: Node v18.20.3
// takes 318,767,104 decimal digits (19 to a part) and refuses one more, and
// 352,321,536 octal (21 to a part), 268,435,456 hexadecimal (16) and
// 2^30 binary (64).
const v8MaxParts = 1 << 24

var v8DigitsPerPart = map[int]int{2: 64, 8: 21, 10: 19, 16: 16}

// checkBlock is how many characters a scan reads between calls of its
// check: about 1 ms of them.
const checkBlock = 1 << 20

// TooLong reports whether V8 refuses digits, in base 2, 8, 10 or 16, as
// too long for a BigInt. Separators ('_', in a literal) are not digits.
func TooLong(digits string, base int) bool {
	return TooLongWith(digits, base, nil)
}

// TooLongWith is TooLong, calling check (if not nil) between blocks of
// characters, so that a caller can interrupt it.
func TooLongWith(digits string, base int, check func()) bool {
	n := 0
	leading := true
	for i := 0; i < len(digits); i++ {
		if check != nil && i%checkBlock == 0 {
			check()
		}
		switch c := digits[i]; {
		case c == '_':
		case leading && c == '0':
		default:
			leading = false
			n++
		}
	}
	return tooManyDigits(n, base)
}

// tooManyDigits reports whether V8 refuses n digits (leading zeros left
// out) in base.
func tooManyDigits(n, base int) bool {
	perPart := v8DigitsPerPart[base]
	return (n+perPart-1)/perPart > v8MaxParts
}

// ParsePow2 returns the value of s, digits in base 2, 8 or 16 and nothing
// else, or false if it is not one, in time linear in its length:
// big.Int.SetString takes time quadratic in it in base 8 (4M digits: 18 s).
func ParsePow2(s string, base int) (*big.Int, bool) {
	return ParsePow2With(s, base, nil)
}

// ParsePow2With is ParsePow2, calling check (if not nil) between blocks of
// characters, so that a caller can interrupt it.
func ParsePow2With(s string, base int, check func()) (*big.Int, bool) {
	if s == "" {
		return nil, false
	}
	shift := bits.TrailingZeros(uint(base))
	words := make([]big.Word, (len(s)*shift+bits.UintSize-1)/bits.UintSize)
	at := 0
	for i := len(s) - 1; i >= 0; i-- {
		if check != nil && i%checkBlock == 0 {
			check()
		}
		d := digitValue(s[i])
		if d >= base {
			return nil, false
		}
		w, off := at/bits.UintSize, at%bits.UintSize
		words[w] |= big.Word(d) << off
		if off+shift > bits.UintSize {
			words[w+1] |= big.Word(d) >> (bits.UintSize - off)
		}
		at += shift
	}
	return new(big.Int).SetBits(words), true
}

// digitValue is the value of the digit c (up to base 16), or 16.
func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 16
}
