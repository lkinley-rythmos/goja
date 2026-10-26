package goja

import (
	"errors"
	"math/big"
	"math/bits"
	"runtime"
	"testing"
)

// allocDuring runs prg under limit (0 for none) and returns what the
// runtime charged and what Go allocated meanwhile.
func allocDuring(t *testing.T, r *Runtime, prg *Program, limit int64) (counted, actual int64, err error) {
	t.Helper()
	// Twice, which empties math/big's pool of scratch.
	runtime.GC()
	runtime.GC()
	r.SetAllocationLimit(limit)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err = r.RunProgram(prg)
	runtime.ReadMemStats(&after)
	return r.AllocatedBytes(), int64(after.TotalAlloc - before.TotalAlloc), err
}

// mulTo makes big.Int.Mul's product in a destination and scratch that hold
// other words before, along every way it splits a product: by blocks of a
// long operand for a short one, and by Karatsuba's method, squares
// included.
func TestBigIntMulParts(t *testing.T) {
	r := New()
	sizes := []int{1, 2, 40, 3000, 8193, 9000, 20000}
	if !testing.Short() && !raceDetectorEnabled {
		sizes = append(sizes, 70000, bigBlockWords+1)
	}
	garbage := func(n int) []big.Word {
		w := make([]big.Word, n)
		for i := range w {
			w[i] = big.Word(uint64(0x9e3779b97f4a7c15) * uint64(i+1))
		}
		return w
	}
	check := func(x, y *big.Int) {
		t.Helper()
		xb, yb := x.Bits(), y.Bits()
		z := garbage(len(xb) + len(yb))
		scratch := garbage(mulScratchWords(max(len(xb), len(yb)), min(len(xb), len(yb))))
		r.mulTo(z, xb, yb, scratch)
		want := new(big.Int).Mul(x, y)
		if new(big.Int).SetBits(natNorm(z)).Cmp(want) != 0 {
			t.Fatalf("%d x %d words: wrong product", len(xb), len(yb))
		}
		if r.mulAbs(xb, yb).Cmp(want) != 0 {
			t.Fatalf("%d x %d words: mulAbs gives a wrong product", len(xb), len(yb))
		}
	}
	for i, a := range sizes {
		for j, b := range sizes {
			if a*b > 1<<28 {
				continue
			}
			check(testBigRand(a, uint64(i), 1<<(bits.UintSize-4)), testBigRand(b, uint64(100+j), 1<<(bits.UintSize-4)))
		}
		if a > 70000 {
			continue
		}
		x := testBigRand(a, uint64(i), 1<<(bits.UintSize-4))
		check(x, x)
		// Halves of zero words, and all bits set.
		w := make([]big.Word, a)
		w[a-1] = 1
		check(new(big.Int).SetBits(w), x)
		for k := range w {
			w[k] = ^big.Word(0)
		}
		check(new(big.Int).SetBits(w), new(big.Int).SetBits(w))
	}
}

// A product and its parts are no larger than what is charged for them:
// x * 3n of 2^20 + 1 words made a second product, uncharged, in blocks of
// 2^20 words (16.8 MB made, 8.4 MB charged); the parts of a balanced
// product were made several times over (y * y of 8193 words: 403 to
// 575 KB made, 131 KB charged). Charged as made, x * 3n is refused under
// a limit of its result's size and a little more.
func TestBigIntMulTemporariesCharged(t *testing.T) {
	r := New()
	if _, err := r.RunString("var x = (1n << 67108927n) - 12345n, y = (1n << 524351n) - 12345n;"); err != nil {
		t.Fatal(err)
	}
	exprs := []string{"x * 3n", "y * y", "y * 12345678901234567890123n", "3n * y * y"}
	if !testing.Short() && !raceDetectorEnabled {
		exprs = append(exprs, "x * y")
	}
	for _, src := range exprs {
		prg := MustCompile("", src, false)
		r.RunProgram(prg) // math/big's pooled scratch, as a script that multiplies has it
		counted, actual, err := allocDuring(t, r, prg, 0)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if actual > counted+64<<10 {
			t.Errorf("%s: made %d bytes, charged %d", src, actual, counted)
		}
	}
	prg := MustCompile("", "x * 3n", false)
	counted, actual, err := allocDuring(t, r, prg, 8388696)
	if err == nil && actual > counted+64<<10 {
		t.Fatalf("x * 3n under its result's size: made %d bytes, charged %d", actual, counted)
	}
	if err != nil && !errors.Is(err, ErrAllocationLimitExceeded) {
		t.Fatal(err)
	}
}

// The parts the other BigInt operations made in rounds of this branch are
// charged too: the powers, quotients and texts of a long BigInt written in
// decimal (2 MB made beyond what was charged for x of 2^20 bits) and the
// strings of a radix that is a power of two; the products and the parts of
// a long string of digits read as a BigInt; the quotient of a long
// remainder; math/big's own scratch; a carry out of a bitwise result.
func TestBigIntPartsCharged(t *testing.T) {
	r := New()
	if _, err := r.RunString(`var x = (1n << 1048576n) + 12345n, y = (1n << 524351n) - 12345n,
		s = "7".repeat(300000), u = "\u3000" + "1".repeat(1 << 20) + "\u3000",
		m = -(1n << 4194304n), ones = (1n << 4194304n) - 1n;`); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{
		"x.toString()", "x.toString(16)", "x.toString(8)", "x.toString(36)", "(-x).toString(32)",
		"BigInt(s)", "BigInt(u)",
		"x / y", "x % y", "x / 3n", "x % 3n", "x % 12345678901234567890123n", "(y * y) % (y + 1n)",
		"y ** 3n", "x * y",
		"m | -1n", "ones ^ -1n", "-ones & -ones",
	} {
		prg := MustCompile("", src, false)
		counted, actual, err := allocDuring(t, r, prg, 0)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if actual > counted+64<<10 {
			t.Errorf("%s: made %d bytes, charged %d", src, actual, counted)
		}
	}
}
