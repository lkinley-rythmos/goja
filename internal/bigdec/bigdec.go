// Package bigdec converts long strings of decimal digits to a big.Int.
package bigdec

import (
	"math/big"
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
	if s == "" {
		return nil, false
	}
	for i := 0; i < len(s); i++ {
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
		p := pows[len(pows)-1]
		pows = append(pows, new(big.Int).Mul(p, p))
	}
	return parse(s, pows, len(pows)-1), true
}

// parse converts s, of at most leafDigits << (i + 1) digits.
func parse(s string, pows []*big.Int, i int) *big.Int {
	if len(s) <= leafDigits {
		return leaf(s)
	}
	for !partShorter(i, len(s)) {
		i--
	}
	// The low part is leafDigits << i digits long, the high one at most as
	// long; leafDigits << i < len(s), so the shift does not overflow.
	k := len(s) - leafDigits<<i
	z := parse(s[:k], pows, i)
	z.Mul(z, pows[i])
	return z.Add(z, parse(s[k:], pows, i-1))
}

// partShorter reports whether leafDigits << i < n, written so that the
// shift cannot overflow an int on a 32-bit platform (n can be up to 2^31 - 1
// there, and leafDigits << 19 is 2^31).
func partShorter(i, n int) bool {
	return n > 0 && leafDigits <= (n-1)>>i
}

func leaf(s string) *big.Int {
	z, _ := new(big.Int).SetString(s, 10)
	return z
}
