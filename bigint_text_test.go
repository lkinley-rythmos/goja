package goja

import (
	"errors"
	"math/big"
	"math/bits"
	"testing"
	"time"
)

// bigText gives big.Int's strings, in every radix and for every way it
// writes them.
func TestBigIntText(t *testing.T) {
	r := New()
	for _, n := range []int{1, 2000, 2049, 5000, 12000} {
		x := testBigRand(n, uint64(n), 1<<(bits.UintSize-2))
		for _, radix := range []int{10, 3, 7, 36, 16} {
			for _, v := range []*big.Int{x, new(big.Int).Neg(x), new(big.Int).Exp(big.NewInt(int64(radix)), big.NewInt(int64(n*20)), nil)} {
				if got := r.bigText(v, radix); got != v.Text(radix) {
					t.Fatalf("%d words in radix %d: wrong", n, radix)
				}
			}
		}
	}
	// Powers of two a block of words at a time.
	for _, n := range []int{1<<16 + 5, 3<<14 + 1, 5<<15 + 3} {
		x := testBigRand(n, uint64(n), 1<<(bits.UintSize-2))
		for _, radix := range []int{2, 4, 8, 16, 32} {
			for _, v := range []*big.Int{x, new(big.Int).Neg(x), new(big.Int).Lsh(big.NewInt(1), uint(64*n-3))} {
				if got := r.bigText(v, radix); got != v.Text(radix) {
					t.Fatalf("%d words in radix %d: wrong", n, radix)
				}
			}
		}
	}
}

// V8 refuses to write a BigInt whose string could be longer than
// String::kMaxLength, estimated from its length in bits (for a radix that is
// a power of two, with the sign counted twice); Node v18.20.3's results.
func TestBigIntTextMaxLength(t *testing.T) {
	const SCRIPT = `
	function invalid(f, what) {
		try { f(); } catch (e) {
			assert(e instanceof RangeError, what);
			assert.sameValue(e.message, "Invalid string length", what);
			return;
		}
		throw new Error(what + ": did not throw");
	}
	invalid(() => (1n << 536870888n).toString(2), "2^536870888 in radix 2");
	invalid(() => (-(1n << 536870887n)).toString(2), "-2^536870887 in radix 2");
	invalid(() => (-(1n << 536870886n)).toString(2), "-2^536870886 in radix 2");
	invalid(() => (1n << 1073741776n).toString(4), "2^1073741776 in radix 4");
	invalid(() => (1n << 838860762n).toString(3), "2^838860762 in radix 3");
	invalid(() => (-(1n << 838860761n)).toString(3), "-2^838860761 in radix 3");
	invalid(() => (-(1n << 838860760n)).toString(3), "-2^838860760 in radix 3");
	assert.sameValue((-(1n << 357913940n)).toString(8).length, 119304648, "-2^357913940 in radix 8");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

// Writing a large BigInt is made in parts, with the interrupt checked
// between them, and its string is charged before it is made:
// big.Int.Text took 8.4 s to write 3n ** 20000000n in decimal.
func TestBigIntTextInterruptible(t *testing.T) {
	for _, src := range []string{"String(x)", "x.toString()", "'' + x", "`${x}`", "x.toString(7)", "new String(x)", "x + ''"} {
		t.Run(src, func(t *testing.T) {
			r := New()
			if _, err := r.RunString("var x = 3n ** 20000000n"); err != nil {
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
	r := New()
	if _, err := r.RunString("var y = 7n ** 1500000n"); err != nil {
		t.Fatal(err)
	}
	r.SetAllocationLimit(1 << 20)
	start := time.Now()
	_, err := r.RunString("String(y)")
	if !errors.Is(err, ErrAllocationLimitExceeded) {
		t.Fatalf("got %v, want the allocation limit", err)
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("took %v", took)
	}
	_ = big.NewInt
}
