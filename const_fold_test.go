package goja

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// compileWithin compiles src and fails unless it finishes within limit and
// allocates less than maxAlloc bytes. It runs the compile in a goroutine so
// that a compile that does not finish fails the test instead of hanging it.
func compileWithin(t *testing.T, src string, limit time.Duration, maxAlloc uint64) *Program {
	t.Helper()
	limit = slack(limit)
	type result struct {
		prg   *Program
		err   error
		alloc uint64
		took  time.Duration
	}
	done := make(chan result, 1)
	go func() {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		prg, err := Compile("", src, false)
		took := time.Since(start)
		runtime.ReadMemStats(&after)
		done <- result{prg, err, after.TotalAlloc - before.TotalAlloc, took}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("compile error: %v", r.err)
		}
		if r.took > limit {
			t.Fatalf("compile took %v, want under %v", r.took, limit)
		}
		if r.alloc > maxAlloc {
			t.Fatalf("compile allocated %d bytes, want under %d", r.alloc, maxAlloc)
		}
		return r.prg
	case <-time.After(limit):
		t.Fatalf("compile did not finish within %v", limit)
	}
	return nil
}

// The compiler folds operations on constants. Folding a BigInt operation
// whose result is large made the compile itself unbounded: 3n ** 100000000n
// took more than 10 s and 1n << 100000000000n asked for 12.5 GB. Node
// compiles all of these at once; they are left to run time.
func TestConstFoldBigIntBounded(t *testing.T) {
	for _, expr := range []string{
		"3n ** 100000000n",
		"1n << 100000000000n",
		"1n << 4000000000n",
		"(-1n) >> -4000000000n",
		"(2n ** 1000n) * (2n ** 1000n) * (2n ** 1000n)",
		"((2n ** 512n) ** 2n) ** 2n",
		"9n ** 100000000n ? 1 : 2",
	} {
		t.Run(expr, func(t *testing.T) {
			compileWithin(t, "function f(x) { return "+expr+"; }", time.Second, 16<<20)
			compileWithin(t, "function f(x) { if ("+expr+") return 1; }", time.Second, 16<<20)
			compileWithin(t, "var o = {["+expr+"]: 1};", time.Second, 16<<20)
		})
	}
}

// A logical expression is constant when its constant left operand
// short-circuits, and unary delete of a constant is constant, so the
// operands of a BigInt operation can be constants without being literals.
func TestConstFoldBigIntShortCircuitBounded(t *testing.T) {
	for _, expr := range []string{
		"(3n || x) ** 60000000n",
		"(3n ?? x) ** 60000000n",
		"((0n && x) - 3n) ** 60000000n",
		"3n ** (60000000n || x)",
		"3n ** (60000000n ?? x)",
		"(delete 1 && 3n) ** 60000000n",
		"(void 0 ?? 3n) << (4000000000n || x)",
	} {
		t.Run(expr, func(t *testing.T) {
			compileWithin(t, "function f(x) { return "+expr+"; }", time.Second, 16<<20)
		})
	}
}

// Evaluating a constant logical expression evaluated its left operand again
// at every level, so a chain of n terms took 2^n steps: 26 took 9 s.
func TestConstFoldLogicalChainLinear(t *testing.T) {
	for _, tc := range []struct{ head, term string }{
		{"3n", " || 0n"},
		{"3n", " ?? 0n"},
		{"0n", " && 0n"},
		{"1", " && 1 || 0"},
		{"null", " ?? undefined"},
	} {
		for _, n := range []int{26, 2000} {
			src := "function f() { return " + tc.head + strings.Repeat(tc.term, n) + "; }"
			t.Run(fmt.Sprintf("%s%s x%d", tc.head, tc.term, n), func(t *testing.T) {
				compileWithin(t, src, time.Second, 64<<20)
			})
		}
	}
	// Nested on the right as well as on the left.
	src := "function f() { return " + strings.Repeat("(0n || ", 1000) + "1n" + strings.Repeat(")", 1000) + "; }"
	compileWithin(t, src, time.Second, 64<<20)
}

// The other folds are bounded the same way: a long chain of string
// concatenations copied the string made so far at every step, a chain of
// unary operators on a long BigInt literal copied the literal at every
// step, and comparing a BigInt with a long string parsed the string, in
// time quadratic in its length.
func TestConstFoldStringAndUnaryBounded(t *testing.T) {
	term := `"` + strings.Repeat("a", 100) + `"`
	chain := term + strings.Repeat(" + "+term, 3000)
	prg := compileWithin(t, "function f() { return "+chain+"; } f()", time.Second, 64<<20)
	v, err := New().RunProgram(prg)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Repeat("a", 300100); v.String() != want {
		t.Fatalf("got a string of %d, want %d", len(v.String()), len(want))
	}

	lit := strings.Repeat("7", 20000) + "n"
	prg = compileWithin(t, "function f() { return "+strings.Repeat("~", 2000)+lit+"; } f() === "+lit, time.Second, 8<<20)
	if v, err := New().RunProgram(prg); err != nil || v != valueTrue {
		t.Fatalf("got %v, %v", v, err)
	}
	prg = compileWithin(t, "function f() { return "+strings.Repeat("- ", 2001)+lit+"; } f() === -"+lit, time.Second, 8<<20)
	if v, err := New().RunProgram(prg); err != nil || v != valueTrue {
		t.Fatalf("got %v, %v", v, err)
	}

	digits := strings.Repeat("7", 2000000)
	compileWithin(t, `function f() { return 1n < "`+digits+`"; }`, time.Second, 64<<20)
	compileWithin(t, `function f() { return 1n == "`+digits+`"; }`, time.Second, 64<<20)
}

// Folding must not change a result: what is left to run time gives the
// value, or throws the error, that the folded constant would have.
func TestConstFoldSkippedSameResult(t *testing.T) {
	const SCRIPT = `
	function check(got, want, what) {
		if (got !== want) throw new Error(what + ": got " + got + ", want " + want);
	}
	check((2n ** 2000n).toString(16), "1" + "0".repeat(500), "2n ** 2000n");
	check(((3n || x) ** 700n) % 1000003n, 3n ** 700n % 1000003n, "short-circuit base");
	check(((2n ** 1000n) * (2n ** 1000n)) >> 1999n, 2n, "product");
	check((1n << 5000n) >> 4999n, 2n, "shift");
	check((-1n) >> -3000n === -(1n << 3000n), true, "negative right shift");
	check(typeof (2n ** 5000n), "bigint", "typeof");
	check(!(2n ** 5000n), false, "not");
	check(-(2n ** 5000n) < 0n, true, "negate");
	check(~(2n ** 5000n) === -(2n ** 5000n) - 1n, true, "bitwise not");
	check("" + (2n ** 5000n) === (2n ** 5000n).toString(), true, "to string");
	check(((2n ** 5000n) ? "t" : "f"), "t", "test");
	check(({[2n ** 1100n]: 1})[(2n ** 1100n).toString()], 1, "computed key");
	check("x".repeat(2000) + "y" === "x".repeat(2000) + "y", true, "string");
	var thrown;
	try { (2n ** 2000n) + 1; } catch (e) { thrown = e; }
	check(thrown instanceof TypeError, true, "mixed types");
	check(thrown.message, "Cannot mix BigInt and other types, use explicit conversions", "mixed types message");
	thrown = undefined;
	try { (2n ** 2000n) / 0n; } catch (e) { thrown = e; }
	check(thrown instanceof RangeError && thrown.message, "Division by zero", "division by zero");
	thrown = undefined;
	try { (2n ** 2000n) ** -1n; } catch (e) { thrown = e; }
	check(thrown instanceof RangeError && thrown.message, "Exponent must be positive", "negative exponent");
	thrown = undefined;
	try { 1n << 100000000000n; } catch (e) { thrown = e; }
	check(thrown instanceof RangeError && thrown.message, "Maximum BigInt size exceeded", "too big");
	thrown = undefined;
	try { 3n ** 100000000000n; } catch (e) { thrown = e; }
	check(thrown instanceof RangeError && thrown.message, "Maximum BigInt size exceeded", "too big power");
	`
	testScript(SCRIPT, _undefined, t)
}

// A chain of !, ~ or typeof on a constant compiles in time linear in its
// length: each was folded from the constant below it, which was emitted
// again from the bottom at every level (8,000 ! took 4.1 s).
func TestConstFoldUnaryChainLinear(t *testing.T) {
	for _, op := range []string{"!", "~", "typeof ", "- ", "void "} {
		src := "function f() { return " + strings.Repeat(op, 8000) + "1; } f()"
		prg := compileWithin(t, src, 500*time.Millisecond, 256<<20)
		v, err := New().RunProgram(prg)
		if err != nil {
			t.Fatal(err)
		}
		var want Value
		switch op {
		case "!":
			want = valueTrue
		case "~", "- ":
			want = intToValue(1)
		case "typeof ":
			want = asciiString("string")
		case "void ":
			want = _undefined
		}
		if !v.SameAs(want) {
			t.Fatalf("%q x 8000: got %v, want %v", op, v, want)
		}
	}
}

// A constant chain of !, ~ or typeof compiles about as fast as the same
// chain on a variable, which is not folded: each level was evaluated on
// its own (8,000 ! took 9.7 ms against 1.8 ms before the fold bounds), as
// the bound of a chain is now taken from its bottom.
func TestConstFoldUnaryChainCost(t *testing.T) {
	best := func(src string) time.Duration {
		d := time.Hour
		for i := 0; i < 5; i++ {
			start := time.Now()
			if _, err := Compile("", src, false); err != nil {
				t.Fatal(err)
			}
			d = min(d, time.Since(start))
		}
		return d
	}
	for _, op := range []string{"!", "~", "typeof "} {
		folded := best("function f() { return " + strings.Repeat(op, 8000) + "1; }")
		plain := best("function f(x) { return " + strings.Repeat(op, 8000) + "x; }")
		if folded > 2*plain+slack(2*time.Millisecond) {
			t.Fatalf("%q x 8000: %v, on a variable %v", op, folded, plain)
		}
	}
}
