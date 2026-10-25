package goja

import (
	"errors"
	"math/big"
	"math/bits"
	"testing"
	"time"
)

func testBigRand(words int, seed uint64, top big.Word) *big.Int {
	w := make([]big.Word, words)
	for i := range w {
		seed = seed*6364136223846793005 + 1442695040888963407
		w[i] = big.Word(seed ^ seed>>29)
	}
	if words > 4 {
		w[words/3] = 0
		w[words/2] = ^big.Word(0)
	}
	w[words-1] = w[words-1]>>4 | top
	return new(big.Int).SetBits(w)
}

// divAbs and bigText give big.Int's quotients, remainders and strings, for
// every way divAbs divides.
func TestBigIntDivAbs(t *testing.T) {
	r := New()
	sizes := []int{1, 2, 3, 100, 127, 128, 129, 4096, 4097, 8191, 8192, 8193, 9000, 20000, 33000, 70000}
	tops := []big.Word{1, 1 << (bits.UintSize - 1)}
	for i, xn := range sizes {
		for j, yn := range sizes {
			if yn > xn {
				continue
			}
			for k, top := range tops {
				x := testBigRand(xn, uint64(i*100+j), tops[1-k])
				y := testBigRand(yn, uint64(j*31+7), top)
				q, m := r.divAbs(x, y)
				wq, wm := new(big.Int).QuoRem(x, y, new(big.Int))
				if q.Cmp(wq) != 0 || m.Cmp(wm) != 0 {
					t.Fatalf("%d / %d words (top %x): wrong", xn, yn, top)
				}
			}
		}
	}
	// Exact quotients, remainders of b - 1, powers of two.
	for _, n := range []int{5000, 9000, 20000} {
		b := testBigRand(n, 3, 1)
		for _, a := range []*big.Int{
			new(big.Int).Mul(b, testBigRand(n+7, 5, 1)),
			new(big.Int).Sub(new(big.Int).Mul(b, testBigRand(3*n, 9, 1)), big.NewInt(1)),
			new(big.Int).Lsh(big.NewInt(1), uint(64*3*n)),
		} {
			for _, d := range []*big.Int{b, new(big.Int).Lsh(big.NewInt(1), uint(64*n-1))} {
				q, m := r.divAbs(a, d)
				wq, wm := new(big.Int).QuoRem(a, d, new(big.Int))
				if q.Cmp(wq) != 0 || m.Cmp(wm) != 0 {
					t.Fatalf("special %d words: wrong", n)
				}
			}
		}
	}
}

// A large division is made in parts, with the interrupt checked between
// them: big.Int.QuoRem took 30 s to divide 2^21 words by 2^20, and 1.9 s to
// divide 2^24 words by almost as many, none of it interruptible.
func TestBigIntDivisionInterruptible(t *testing.T) {
	const setup = "var x = ((1n << 134217000n) - 1n) / 3n, y = (1n << 67108000n) / 7n + 5n;"
	for _, src := range []string{"x / y", "x % y", "-x / y", "x % -y"} {
		t.Run(src, func(t *testing.T) {
			r := New()
			if _, err := r.RunString(setup); err != nil {
				t.Fatal(err)
			}
			prg := MustCompile("", src, false)
			delay := 300 * time.Millisecond
			timer := time.AfterFunc(delay, func() { r.Interrupt("stop") })
			defer timer.Stop()
			start := time.Now()
			_, err := r.RunProgram(prg)
			took := time.Since(start)
			var ie *InterruptedError
			if !errors.As(err, &ie) {
				t.Fatalf("got %v, want interrupted", err)
			}
			if took > delay+250*time.Millisecond {
				t.Fatalf("interrupted after %v, %v late", took, took-delay)
			}
		})
	}
	// A short quotient of long operands is made in time linear in them.
	r := New()
	start := time.Now()
	v, err := r.RunString("var z = (1n << (2n ** 30n - 1n)) - 12345n; var w = z >> 3n; [z / w, z % w < w, (z - z % w) / w === 8n, (z - z % w) % w === 0n]")
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("took %v", took)
	}
	if got := v.String(); got != "8,true,true,true" {
		t.Fatalf("got %s", got)
	}
}

// A long number divided by a short one is made a block of quotient words
// at a time by big.Int.QuoRem (divChunked), in about the time QuoRem takes
// for the whole of it: dividing 2^24 words by one took 4.7 s block by block
// from the divisor's reciprocal.
func TestBigIntDivisionShortDivisor(t *testing.T) {
	r := New()
	start := time.Now()
	v, err := r.RunString("var w = (1n << (2n ** 30n - 2n)) - 1n; [w / 3n === (w - w % 3n) / 3n, w % 3n, w / 1000003n > 0n, (w / ((1n << 4000n) + 1n)) % 7n >= 0n].join()")
	if err != nil {
		t.Fatal(err)
	}
	// The divisions by one word take about 0.25 s each, the one by 63
	// words 2 s (big.Int.QuoRem as long).
	if took := time.Since(start); took > 8*time.Second {
		t.Fatalf("took %v", took)
	}
	if got := v.String(); got != "true,0,true,true" {
		t.Fatalf("got %s", got)
	}
}
