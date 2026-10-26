package goja

import (
	"errors"
	"math/big"
	"math/bits"
	"runtime"
	"strings"
	"testing"
	"time"
)

// interruptWithin runs src, interrupted after delay, and fails unless it
// returns, interrupted, within bound of the interrupt.
func interruptWithin(t *testing.T, r *Runtime, src string, delay, bound time.Duration) {
	t.Helper()
	prg := MustCompile("", src, false)
	fired := make(chan time.Time, 1)
	timer := time.AfterFunc(delay, func() { fired <- time.Now(); r.Interrupt("stop") })
	_, err := r.RunProgram(prg)
	end := time.Now()
	if timer.Stop() {
		t.Fatalf("%s: returned %v before the interrupt", src, err)
	}
	r.ClearInterrupt()
	var ie *InterruptedError
	if !errors.As(err, &ie) {
		t.Fatalf("%s: got %v, want interrupted", src, err)
	}
	if lag := end.Sub(<-fired); lag > bound {
		t.Fatalf("%s: returned %v after the interrupt", src, lag)
	}
}

// The temporaries of a BigInt operation as long as its operands are charged
// before they are made, the result being shorter: the product and remainder
// of a division with a short quotient, the copies of negative operands of a
// bitwise operation (made by big.Int too, below the length where they are
// made a block at a time); a negative number shifted right is made without
// a copy. x / y, 1n & nx and nx | -1n of numbers near V8's limit made
// 268 MB and 134 MB under a 1 MiB limit, charged 40 bytes.
func TestBigIntTemporariesCharged(t *testing.T) {
	const words = 20000
	r := New()
	if _, err := r.RunString("var x = (1n << 1280000n) - 1n, y = x >> 1n, nx = -x, sx = (1n << 64000n) - 1n, nsx = -sx, p = -(1n << 1280000n), p1 = p - 1n;"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		src   string
		words int // charged at least
	}{
		{"x / y", 2 * words},
		{"x % y", 2 * words},
		{"1n & nx", words},
		{"nx | -1n", words},
		{"nx ^ 1n", words},
		{"nx & nx", 2 * words},
		{"1n & nsx", 1000},
		{"nsx | -1n", 1000},
		{"x / 3n", words},
		{"x % 3n", words},
		{"sx % 3n", 1000},
	} {
		r.SetAllocationLimit(0)
		if _, err := r.RunString(c.src); err != nil {
			t.Fatal(err)
		}
		if got, want := r.AllocatedBytes(), int64(c.words*bits.UintSize/8); got < want {
			t.Errorf("%s: charged %d, want at least %d", c.src, got, want)
		}
	}
	// -ceil(|x| / 2^n), without a copy of x.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	v, err := r.RunString("[nx >> 1279000n, nx >> 1280000n, p >> 1279999n, p1 >> 1280000n].join()")
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != strings.Join([]string{new(big.Int).Rsh(new(big.Int).Neg(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 1280000), big.NewInt(1))), 1279000).String(), "-1", "-2", "-2"}, ",") {
		t.Fatalf("got %s", got)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 64<<10 {
		t.Fatalf("allocated %d bytes", alloc)
	}

	skipHeavy(t)
	r = New()
	if _, err := r.RunString("var x = (1n << 1073741760n) - 1n, y = x >> 1n, nx = -x;"); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"x / y", "x % y", "1n & nx", "nx | -1n"} {
		prg := MustCompile("", src, false)
		runtime.GC()
		r.SetAllocationLimit(1 << 20)
		runtime.ReadMemStats(&before)
		_, err := r.RunProgram(prg)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, ErrAllocationLimitExceeded) {
			t.Fatalf("%s: got %v, want the allocation limit", src, err)
		}
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
			t.Fatalf("%s: allocated %d bytes", src, alloc)
		}
	}
}

// A division with a short quotient of numbers near V8's limit returns soon
// after an interrupt: its product, a word by 2^24 words, and the
// subtraction and corrections that follow it are made a block at a time
// (x / y returned 71 to 78 ms after an interrupt), as are those of a long
// quotient and of the right shift of a negative number.
func TestBigIntShortQuotientInterruptible(t *testing.T) {
	skipHeavy(t)
	r := New()
	if _, err := r.RunString("var x = (1n << 1073741760n) - 1n, y = x >> 1n, nx = -x, h = (1n << 536870880n) - 12345n;"); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"x / y", "x % y", "x / h", "x % h", "x * 3n", "nx >> 100n"} {
		runtime.GC()
		interruptWithin(t, r, src, 5*time.Millisecond, bigIntStepSlack)
	}
}

// The linear steps made a block at a time give big.Int's results, across
// the blocks' boundaries, and the products and divisions made from them
// too.
func TestBigIntNatSteps(t *testing.T) {
	r := New()
	long := bigBlockWords + 3
	x := testBigRand(long, 11, 1)
	y := testBigRand(long-1, 12, 1<<(bits.UintSize-1))
	short := testBigRand(3, 13, 1)
	pow := new(big.Int).Lsh(big.NewInt(1), uint(long*bits.UintSize))
	check := func(name string, got, want *big.Int) {
		t.Helper()
		if got.Cmp(want) != 0 {
			t.Fatalf("%s: wrong", name)
		}
	}
	check("natAdd", new(big.Int).SetBits(r.natAdd(x.Bits(), y.Bits())), new(big.Int).Add(x, y))
	check("bigSubAbs", r.bigSubAbs(x.Bits(), y.Bits()), new(big.Int).Sub(x, y))
	check("bigSubAbs negative", r.bigSubAbs(y.Bits(), x.Bits()), new(big.Int).Sub(y, x))
	for _, s := range []uint{0, 1, 63, 64, 65, 1000} {
		check("natShl", new(big.Int).SetBits(r.natShl(x.Bits(), s)), new(big.Int).Lsh(x, s))
		check("natShr", new(big.Int).SetBits(r.natShr(x.Bits(), s)), new(big.Int).Rsh(x, s))
		nx := new(big.Int).Neg(x)
		check("bigShift negative", r.bigShift(nx, big.NewInt(int64(s)), true), new(big.Int).Rsh(nx, s))
	}
	for _, words := range []int{1, 2, 5, 100} {
		for i, top := range []big.Word{1, ^big.Word(0)} {
			nx := new(big.Int).Neg(testBigRand(words, uint64(words+i), top))
			p := new(big.Int).Lsh(big.NewInt(-1), uint(words*bits.UintSize-1))
			for _, s := range []uint{1, 2, 63, 64, 65, 200} {
				for _, v := range []*big.Int{nx, p, new(big.Int).Sub(p, big.NewInt(1)), big.NewInt(-1)} {
					check("bigShift negative short", r.bigShift(v, big.NewInt(int64(s)), true), new(big.Int).Rsh(v, s))
				}
			}
		}
	}
	check("powSub", r.powSub(long, y.Bits()), new(big.Int).Sub(pow, y))
	check("powSub negative", r.powSub(long-1, x.Bits()), new(big.Int).Sub(new(big.Int).Rsh(pow, bits.UintSize), x))
	check("powSub zero", r.powSub(5, nil), new(big.Int).Lsh(big.NewInt(1), 5*bits.UintSize))
	for _, m := range []*big.Int{short, big.NewInt(7), testBigRand(64, 14, 1), testBigRand(65, 15, 1)} {
		check("mulAbs", r.mulAbs(x.Bits(), m.Bits()), new(big.Int).Mul(x, m))
	}
	// Carries through words of all ones, and a quotient corrected both
	// ways.
	ones := new(big.Int).Sub(pow, big.NewInt(1))
	check("natAdd carry", new(big.Int).SetBits(r.natAdd(ones.Bits(), []big.Word{1})), pow)
	check("mulAbs carry", r.mulAbs(ones.Bits(), ones.Bits()[:2]), new(big.Int).Mul(ones, new(big.Int).SetBits(ones.Bits()[:2])))
	for _, a := range []*big.Int{x, ones, new(big.Int).Add(new(big.Int).Mul(y, big.NewInt(3)), big.NewInt(1)), new(big.Int).Sub(new(big.Int).Mul(y, big.NewInt(3)), big.NewInt(1))} {
		q, m := r.divAbs(a, y)
		wq, wm := new(big.Int).QuoRem(a, y, new(big.Int))
		check("divAbs quotient", q, wq)
		check("divAbs remainder", m, wm)
	}
}
