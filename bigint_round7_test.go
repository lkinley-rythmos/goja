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

// skipRaceAlloc skips a test that holds what an operation makes to what it
// is charged: under the race detector, sync.Pool drops a quarter of what is
// put back in it, so math/big makes its scratch again and again in one
// operation (y * y made 948 KB, charged 787 KB, under it).
func skipRaceAlloc(t *testing.T) {
	t.Helper()
	if raceDetectorEnabled {
		t.Skip("math/big's pooled scratch is dropped at random under the race detector")
	}
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
	skipRaceAlloc(t)
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
	skipRaceAlloc(t)
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

// The messages that hold a script's value are made without a Go string of
// it: Date's "Invalid hint" with a symbol's description of 2^27 characters
// made 268 MB under a 1 MiB limit and returned 82 ms after an interrupt,
// and with a hint of 2^26 units of U+3000 made 1.8 GB and returned 1.3 s
// after one. The same holds for the messages valueText wrote through fmt.
func TestMessageValuesBounded(t *testing.T) {
	const setup = `var U = "　".repeat(1 << 22), S = Symbol("x".repeat(1 << 23)), E = new Error(U),
		O = {toString() { return U }}, F = function() {}, T = {};
		Object.defineProperty(F, "name", {value: U}); Object.defineProperty(T, "a", {value: U});`
	srcs := []string{
		"new Date()[Symbol.toPrimitive](S)",
		"new Date()[Symbol.toPrimitive](U)",
		"new Date()[Symbol.toPrimitive](E)",
		"Reflect.ownKeys(new Proxy({}, {ownKeys() { return [E] } }))",
		"Number.prototype.valueOf.call(U)",
		"Symbol.keyFor(U)",
		"Object.defineProperty({}, 'a', U)",
		"Object.create(U)",
		"Object.setPrototypeOf({}, U)",
		"[{toLocaleString: 1, toString() { return U }}].toLocaleString()",
		"Object.getOwnPropertyDescriptor(ArrayBuffer.prototype, 'byteLength').get.call(O)",
		"Uint8Array.from.call(function() { return O }, [1])",
		"Reflect.construct(Int16Array, [new ArrayBuffer(4), 1], F)",
		"Map.prototype.clear.call(U)",
		"new Proxy(T, {get() { return 1 }}).a",
	}
	r := New()
	if _, err := r.RunString(setup); err != nil {
		t.Fatal(err)
	}
	for _, src := range srcs {
		counted, actual, err := allocDuring(t, r, MustCompile("", src, false), 1<<20)
		if !errors.Is(err, ErrAllocationLimitExceeded) {
			t.Errorf("%s: got %v, want the allocation limit", src, err)
		}
		if actual > 2<<20 {
			t.Errorf("%s: made %d bytes, charged %d", src, actual, counted)
		}
	}
	if raceDetectorEnabled {
		return
	}
	r.SetAllocationLimit(0)
	if _, err := r.RunString(`U = "　".repeat(1 << 25); S = Symbol("x".repeat(1 << 26)); E = new Error(U);
		Object.defineProperty(F, "name", {value: U});`); err != nil {
		t.Fatal(err)
	}
	for _, src := range srcs[:3] {
		interruptWithin(t, r, src, 5*time.Millisecond, 20*time.Millisecond)
	}
	for _, src := range []string{"Number.prototype.valueOf.call(U)", "Reflect.construct(Int16Array, [new ArrayBuffer(4), 1], F)"} {
		interruptWithin(t, r, src, 5*time.Millisecond, 20*time.Millisecond)
	}
}

// The values in the messages of Date's "Invalid hint" and of an invalid key
// from a proxy's ownKeys are written as Node v18.20.3 writes them: a BigInt
// or Symbol object and a typed array by their class, a proxy as its target
// (a callable one as its function's source), a long function cut, a bound
// one as V8 writes any, an error's name and message only when they are
// strings, and a symbol's or a string's code units as they are (an
// unpaired surrogate was lost to U+FFFD).
func TestMessageValuesAsNode(t *testing.T) {
	const script = `
	const rev = Proxy.revocable({}, {}); rev.revoke();
	const vals = [Object(1n), Object(Symbol("q")), new Uint8Array(1), new BigInt64Array(1), new Proxy(function(){}, {}),
		new Proxy([], {}), new Proxy(new Proxy(function(){}, {}), {}), rev.proxy, new Proxy(new Error("pe"), {}),
		function f(){}.bind(null), eval("(function(){" + "a".repeat(117) + "})"), Object.assign(new Error("m"), {name: undefined}),
		Object.assign(new Error("m"), {name: 5}), Object.assign(new Error(), {message: 5}), {toString: Error.prototype.toString},
		Object.assign(Object.create(null), {[Symbol.toStringTag]: "Tg"}), new DataView(new ArrayBuffer(1)), (function*(){})(),
		"\ud800", Symbol("\ud800"), Symbol(), Object.assign(new Error(), {name: "\ud800", message: "\udc00"})];
	const out = [];
	for (const v of vals) {
		try { new Date()[Symbol.toPrimitive](v) } catch (e) { out.push(e.message) }
		try { Reflect.ownKeys(new Proxy({}, {ownKeys() { return [v] }})) } catch (e) { out.push(e.message) }
	}
	out.map(m => Array.from(m, c => c.charCodeAt(0) > 0xd7ff && c.charCodeAt(0) < 0xe000 ? "<" + c.charCodeAt(0) + ">" : c).join("")).join("\n")`
	// From Docker Node v18.20.3 (rcx-go-crud-parity-gap-goja-rle-api-1).
	const want = `Invalid hint: [object BigInt]
[object BigInt] is not a valid property name
Invalid hint: [object Symbol]
[object Symbol] is not a valid property name
Invalid hint: [object Uint8Array]
[object Uint8Array] is not a valid property name
Invalid hint: [object BigInt64Array]
[object BigInt64Array] is not a valid property name
Invalid hint: function(){}
function(){} is not a valid property name
Invalid hint: [object Array]
[object Array] is not a valid property name
Invalid hint: function(){}
function(){} is not a valid property name
Invalid hint: null
null is not a valid property name
Invalid hint: Error: pe
Error: pe is not a valid property name
Invalid hint: function () { [native code] }
function () { [native code] } is not a valid property name
Invalid hint: function(){aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa...<omitted>...a}
function(){aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa...<omitted>...a} is not a valid property name
Invalid hint: m
m is not a valid property name
Invalid hint: m
m is not a valid property name
Invalid hint: Error
Error is not a valid property name
Invalid hint: 
 is not a valid property name
Invalid hint: [object Tg]
[object Tg] is not a valid property name
Invalid hint: #<DataView>
#<DataView> is not a valid property name
Invalid hint: [object Generator]
[object Generator] is not a valid property name
Invalid hint: <55296>
Invalid hint: Symbol(<55296>)
Invalid hint: Symbol()
Invalid hint: <55296>: <56320>
<55296>: <56320> is not a valid property name`
	v, err := New().RunString(script)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != want {
		gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := range max(len(gl), len(wl)) {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Errorf("line %d: got %q, want %q", i, g, w)
			}
		}
	}
}
