package bigdec

import (
	"math"
	"math/big"
	"math/bits"
	"strings"
	"testing"
	"time"
)

func TestParseAsSetString(t *testing.T) {
	digits := "31415926535897932384626433832795028841971693993751"
	for _, n := range []int{1, 19, 20, leafDigits - 1, leafDigits, leafDigits + 1, 2 * leafDigits, 2*leafDigits + 1, 3*leafDigits + 7, 9*leafDigits - 3, 70001} {
		s := strings.Repeat(digits, n/len(digits)+1)[:n]
		s = "9" + s[1:]
		want, _ := new(big.Int).SetString(s, 10)
		got, ok := Parse(s)
		if !ok || got.Cmp(want) != 0 {
			t.Fatalf("%d digits: wrong value", n)
		}
		// Leading zeros, and zeros at the start of a part.
		z := strings.Repeat("0", n) + s[:n/2] + strings.Repeat("0", leafDigits+3)
		want, _ = new(big.Int).SetString(z, 10)
		if got, ok := Parse(z); !ok || got.Cmp(want) != 0 {
			t.Fatalf("%d digits with zeros: wrong value", len(z))
		}
	}
	for _, s := range []string{"", "12a", "-1", "+1", "1_000", strings.Repeat("1", 3*leafDigits) + "x"} {
		if _, ok := Parse(s); ok {
			t.Fatalf("%.20q: parsed", s)
		}
	}
}

// big.Int.SetString takes time quadratic in the number of decimal digits:
// 2M take it about 6 s, Parse about 0.7 s (more when other tests run).
func TestParseLong(t *testing.T) {
	s := strings.Repeat("7", 2000000)
	start := time.Now()
	z, ok := Parse(s)
	took := time.Since(start)
	t.Logf("2M digits: %v", took)
	if took > 4*time.Second {
		t.Fatalf("took %v", took)
	}
	if !ok {
		t.Fatal("not parsed")
	}
	// 7...7 (2M digits) = 7 * (10^2000000 - 1) / 9
	want := new(big.Int).Exp(big.NewInt(10), big.NewInt(2000000), nil)
	want.Sub(want, big.NewInt(1)).Mul(want, big.NewInt(7)).Quo(want, big.NewInt(9))
	if z.Cmp(want) != 0 {
		t.Fatal("wrong value")
	}
}

// partShorter compares without overflowing at any length.
func TestPartShorter(t *testing.T) {
	for _, c := range []struct {
		i, n int
		want bool
	}{
		{0, 0, false},
		{0, leafDigits, false},
		{0, leafDigits + 1, true},
		{1, 2 * leafDigits, false},
		{1, 2*leafDigits + 1, true},
		{19, math.MaxInt32, false}, // leafDigits << 19 is 2^31
		{18, math.MaxInt32, true},
		{30, math.MaxInt32, false},
		{62, math.MaxInt, false},
		{50, math.MaxInt, true},
		{51, math.MaxInt, false}, // leafDigits << 51 is 2^63
	} {
		if c.i >= bits.UintSize {
			continue
		}
		if got := partShorter(c.i, c.n); got != c.want {
			t.Errorf("partShorter(%d, %d) = %v, want %v", c.i, c.n, got, c.want)
		}
	}
}
