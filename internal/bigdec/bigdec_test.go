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

// TooLong counts the digits as V8 does (Node v18.20.3's limits).
func TestTooLong(t *testing.T) {
	for _, c := range []struct {
		n, base int
		want    bool
	}{
		{318767104, 10, false},
		{318767105, 10, true},
		{352321536, 8, false},
		{352321537, 8, true},
		{268435456, 16, false},
		{268435457, 16, true},
		{1 << 30, 2, false},
		{1<<30 + 1, 2, true},
	} {
		if got := tooManyDigits(c.n, c.base); got != c.want {
			t.Errorf("%d digits in base %d: got %v", c.n, c.base, got)
		}
	}
	// Leading zeros and separators are not counted.
	ones := strings.Repeat("1", 268435456)
	if TooLong("000"+ones, 16) || !TooLong(ones+"1", 16) || TooLong("1_000", 10) || TooLong("0_0_1", 10) {
		t.Error("wrong count")
	}
}

// ParsePow2 gives big.Int.SetString's values, in linear time.
func TestParsePow2(t *testing.T) {
	for _, base := range []int{2, 8, 16} {
		for _, n := range []int{1, 2, 20, 21, 22, 63, 64, 65, 1000, 4099} {
			s := strings.Repeat("1234567fedcbaFEDCBA0", n/20+1)[:n]
			s = strings.Map(func(r rune) rune {
				if d := digitValue(byte(r)); d >= base {
					return rune('0' + d%base)
				}
				return r
			}, s)
			want, _ := new(big.Int).SetString(s, base)
			got, ok := ParsePow2(s, base)
			if !ok || got.Cmp(want) != 0 {
				t.Fatalf("%d digits in base %d: wrong", n, base)
			}
			if _, ok := ParsePow2(s+"g", base); ok {
				t.Fatalf("%q in base %d: parsed", s+"g", base)
			}
		}
	}
	if _, ok := ParsePow2("", 16); ok {
		t.Fatal("empty: parsed")
	}
	if _, ok := ParsePow2("8", 8); ok {
		t.Fatal("8 in base 8: parsed")
	}
	start := time.Now()
	ParsePow2(strings.Repeat("7", 4000000), 8)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("4M octal digits took %v", took)
	}
}
