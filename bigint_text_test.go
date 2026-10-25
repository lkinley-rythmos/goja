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
	skipHeavy(t)
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
	skipHeavy(t)
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
	skipHeavy(t)
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
			if took > delay+slack(250*time.Millisecond) {
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
	if took := time.Since(start); took > slack(100*time.Millisecond) {
		t.Fatalf("took %v", took)
	}
	_ = big.NewInt
}

// Every conversion of a BigInt to a string a script makes goes through
// bigString: a property key, join, the String.prototype methods and
// global functions that take a string, an error message. With a BigInt of
// 2^30 bits (decimal: 323M digits, which big.Int.Text writes in minutes)
// each is interrupted within milliseconds, and refused by a small
// allocation limit at once.
func TestBigIntTextConversionsRouted(t *testing.T) {
	skipHeavy(t)
	const setup = "var x = (1n << (2n ** 30n - 1n)) - 12345n, o = {};"
	for _, c := range []struct {
		src     string
		latency time.Duration
	}{
		{"o[x]", 30 * time.Millisecond},
		{"[x].join()", 30 * time.Millisecond},
		{"o[x] = 1", 100 * time.Millisecond},
		{"x in o", 100 * time.Millisecond},
		{"delete o[x]", 100 * time.Millisecond},
		{"({[x]: 1})", 100 * time.Millisecond},
		{"Object.defineProperty(o, x, {value: 1})", 100 * time.Millisecond},
		{"o.hasOwnProperty(x)", 100 * time.Millisecond},
		{"Reflect.get(o, x)", 100 * time.Millisecond},
		{"[x].toString()", 100 * time.Millisecond},
		{"[x, 1n].sort()", 100 * time.Millisecond},
		{"'a'.concat(x)", 100 * time.Millisecond},
		{"'a'.padEnd(5, x)", 100 * time.Millisecond},
		{"parseInt(x)", 100 * time.Millisecond},
		{"encodeURIComponent(x)", 100 * time.Millisecond},
		{"new Error(x)", 100 * time.Millisecond},
		{"Symbol(x)", 100 * time.Millisecond},
		{"new BigInt64Array(1).join(x)", 100 * time.Millisecond},
		{"'a' in x", 100 * time.Millisecond},
	} {
		t.Run(c.src, func(t *testing.T) {
			r := New()
			if _, err := r.RunString(setup); err != nil {
				t.Fatal(err)
			}
			delay := 300 * time.Millisecond
			timer := time.AfterFunc(delay, func() { r.Interrupt("stop") })
			defer timer.Stop()
			start := time.Now()
			_, err := runWithin(t, r, c.src, 5*time.Second)
			took := time.Since(start)
			var ie *InterruptedError
			if !errors.As(err, &ie) {
				t.Fatalf("got %v, want interrupted", err)
			}
			if took > delay+slack(c.latency) {
				t.Fatalf("interrupted %v late", took-delay)
			}

			r = New()
			if _, err := r.RunString(setup); err != nil {
				t.Fatal(err)
			}
			r.SetAllocationLimit(1 << 20)
			start = time.Now()
			_, err = runWithin(t, r, c.src, 5*time.Second)
			if !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("got %v, want the allocation limit", err)
			}
			if took := time.Since(start); took > slack(500*time.Millisecond) {
				t.Fatalf("took %v", took)
			}
		})
	}
	// The conversions give the same strings.
	testScript(`var o = {}; var k = 2n ** 70n; o[k] = 1;
	[Object.keys(o)[0] === String(k), [k, 3n].join() === k + ",3", o.hasOwnProperty(k), (k in o), 'a'.concat(10n), parseInt(77n), new Error(5n).message,
	 ({[5n]: 2})["5"], [10n, 9n].sort().join(), String(Symbol(4n))].join()`,
		asciiString("true,true,true,true,a10,77,5,2,10,9,Symbol(4)"), t)
}

// An object key whose ToPrimitive (hint string) is a BigInt is written by
// bigString too: through toString, Symbol.toPrimitive, or valueOf.
func TestBigIntTextObjectKeys(t *testing.T) {
	skipHeavy(t)
	const setup = "var x = (1n << (2n ** 30n - 1n)) - 12345n, o = {};" +
		"var kt = {toString() { return x }}, kp = {[Symbol.toPrimitive]() { return x }}, kv = {toString: null, valueOf() { return x }};"
	for _, src := range []string{"o[kt]", "kt in o", "o[kp]", "kp in o", "o[kv]", "kv in o", "delete o[kt]", "o.hasOwnProperty(kt)", "o.propertyIsEnumerable(kt)", "Reflect.has(o, kt)", "({[kt]: 1})"} {
		t.Run(src, func(t *testing.T) {
			r := New()
			if _, err := r.RunString(setup); err != nil {
				t.Fatal(err)
			}
			delay := 300 * time.Millisecond
			timer := time.AfterFunc(delay, func() { r.Interrupt("stop") })
			defer timer.Stop()
			start := time.Now()
			_, err := runWithin(t, r, src, 5*time.Second)
			took := time.Since(start)
			var ie *InterruptedError
			if !errors.As(err, &ie) {
				t.Fatalf("got %v, want interrupted", err)
			}
			if took > delay+slack(30*time.Millisecond) {
				t.Fatalf("interrupted %v late", took-delay)
			}

			r = New()
			if _, err := r.RunString(setup); err != nil {
				t.Fatal(err)
			}
			r.SetAllocationLimit(1 << 20)
			_, err = runWithin(t, r, src, 5*time.Second)
			if !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("got %v, want the allocation limit", err)
			}
		})
	}
	testScript(`var o = {"12": 1}, k = {toString() { return 12n }}, n = 0, c = {toString() { n++; return "12" }};
	[o[k], k in o, ({[k]: 2})["12"], 1 in [5, 6], 2 in [5, 6], c in o, n].join()`, asciiString("1,true,2,true,false,true,1"), t)
}
