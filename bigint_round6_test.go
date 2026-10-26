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

// The messages of BigInt(symbol), of an invalid key from a proxy's
// ownKeys and of the other conversions that print a value are Node
// v18.20.3's: a symbol's description whole, its code units as they are
// (BigInt(Symbol("\ud800")) lost its unpaired surrogate to U+FFFD), a
// BigInt key in decimal and any other as V8 writes it
// (NoSideEffectsToString), and Reflect.set converts its key as a property
// key.
func TestBigIntMessagesAsNode(t *testing.T) {
	compareLines(t, runLogged(t, bigIntMessagesScript), bigIntMessagesNode)
}

const bigIntMessagesScript = `const show = (f) => { try { const r = f(); return typeof r === 'bigint' ? r + 'n' : typeof r + ':' + String(r); } catch (e) { const m = e.message; return e.name + ': ' + (m.length > 60 ? m.length + ' ' + m.slice(0, 30) + '...' + m.slice(-20) : m) + ' [' + [m.charCodeAt(22), m.charCodeAt(23)].join() + ']'; } };
const cases = [
  ['Symbol()', () => BigInt(Symbol())],
  ['Symbol("")', () => BigInt(Symbol(""))],
  ['Symbol("s")', () => BigInt(Symbol("s"))],
  ['Symbol(lone)', () => BigInt(Symbol("\ud800"))],
  ['Symbol(lone mid)', () => BigInt(Symbol("ab\udc00c"))],
  ['Symbol(pair)', () => BigInt(Symbol("\u{1F9EA}x"))],
  ['Symbol(2000)', () => BigInt(Symbol("y".repeat(2000)))],
  ['Symbol(unicode 2000)', () => BigInt(Symbol("\u3000".repeat(2000)))],
  ['Object(Symbol)', () => BigInt(Object(Symbol("o")))],
  ['asIntN Symbol', () => BigInt.asIntN(8, Symbol("a"))],
  ['ta Symbol', () => { const a = new BigInt64Array(1); a[0] = Symbol("t"); }],
  ['ownKeys 1n', () => Reflect.ownKeys(new Proxy({}, {ownKeys() { return [12345678901234567890n] }}))],
  ['ownKeys -5n', () => Reflect.ownKeys(new Proxy({}, {ownKeys() { return [-5n] }}))],
  ['ownKeys 1.5', () => Reflect.ownKeys(new Proxy({}, {ownKeys() { return [1.5] }}))],
  ['ownKeys undefined', () => Reflect.ownKeys(new Proxy({}, {ownKeys() { return [undefined] }}))],
  ['ownKeys null', () => Reflect.ownKeys(new Proxy({}, {ownKeys() { return [null] }}))],
  ['ownKeys true', () => Reflect.ownKeys(new Proxy({}, {ownKeys() { return [true] }}))],
  ['ownKeys {}', () => Reflect.ownKeys(new Proxy({}, {ownKeys() { return [{}] }}))],
  ['ownKeys toString', () => Reflect.ownKeys(new Proxy({}, {ownKeys() { return [{toString() { return "zz" }}] }}))],
  ['ownKeys keys', () => Object.keys(new Proxy({}, {ownKeys() { return [7n] }}))],
  ['hint 1n', () => new Date(0)[Symbol.toPrimitive](12n)],
  ['hint 5', () => new Date(0)[Symbol.toPrimitive](5)],
  ['hint {}', () => new Date(0)[Symbol.toPrimitive]({})],
  ['setPrototypeOf 12n', () => Object.setPrototypeOf({}, 12n)],
  ['Reflect.set 12n', () => { const o = {}; return Reflect.set(o, 12n, 1) + ' ' + Object.keys(o) + ' ' + o[12]; }],
  ['Reflect.set obj key', () => { const o = {}; Reflect.set(o, {toString() { return "k" }}, 1); return Object.keys(o).join(); }],
];
for (const [k, f] of cases) console.log(k, '=>', show(f));
`

const bigIntMessagesNode = "Symbol() => TypeError: Cannot convert Symbol() to a BigInt [41,32]\nSymbol(\"\") => TypeError: Cannot convert Symbol() to a BigInt [41,32]\nSymbol(\"s\") => TypeError: Cannot convert Symbol(s) to a BigInt [115,41]\nSymbol(lone) => TypeError: Cannot convert Symbol(\ufffd) to a BigInt [55296,41]\nSymbol(lone mid) => TypeError: Cannot convert Symbol(ab\ufffdc) to a BigInt [97,98]\nSymbol(pair) => TypeError: Cannot convert Symbol(\U0001f9eax) to a BigInt [55358,56810]\nSymbol(2000) => TypeError: 2035 Cannot convert Symbol(yyyyyyyy...yyyyyyy) to a BigInt [121,121]\nSymbol(unicode 2000) => TypeError: 2035 Cannot convert Symbol(\u3000\u3000\u3000\u3000\u3000\u3000\u3000\u3000...\u3000\u3000\u3000\u3000\u3000\u3000\u3000) to a BigInt [12288,12288]\nObject(Symbol) => TypeError: Cannot convert Symbol(o) to a BigInt [111,41]\nasIntN Symbol => TypeError: Cannot convert Symbol(a) to a BigInt [97,41]\nta Symbol => TypeError: Cannot convert Symbol(t) to a BigInt [116,41]\nownKeys 1n => TypeError: 12345678901234567890 is not a valid property name [115,32]\nownKeys -5n => TypeError: -5 is not a valid property name [101,114]\nownKeys 1.5 => TypeError: 1.5 is not a valid property name [112,101]\nownKeys undefined => TypeError: undefined is not a valid property name [105,100]\nownKeys null => TypeError: null is not a valid property name [111,112]\nownKeys true => TypeError: true is not a valid property name [111,112]\nownKeys {} => TypeError: #<Object> is not a valid property name [105,100]\nownKeys toString => TypeError: [object Object] is not a valid property name [32,97]\nownKeys keys => TypeError: 7 is not a valid property name [114,116]\nhint 1n => TypeError: Invalid hint: 12 [NaN,NaN]\nhint 5 => TypeError: Invalid hint: 5 [NaN,NaN]\nhint {} => TypeError: Invalid hint: #<Object> [62,NaN]\nsetPrototypeOf 12n => TypeError: Object prototype may only be an Object or null: 12 [110,108]\nReflect.set 12n => string:true 12 1\nReflect.set obj key => string:k"

// A symbol's description is put in BigInt(symbol)'s message as a String,
// charged before it is made and a block at a time, the interrupt checked
// between them: a description of 2^27 characters made a message of 134 MB
// in 519 ms after an interrupt, charged 256 bytes. A description imported
// from Go is read a block at a time too.
func TestBigIntSymbolMessageBounded(t *testing.T) {
	r := New()
	r.Set("imported", strings.Repeat("\u3000x", 1<<20))
	if _, err := r.RunString(`var s = Symbol("x".repeat(1 << 22)), si = Symbol(imported);`); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"BigInt(s)", "BigInt(si)"} {
		r.SetAllocationLimit(1 << 20)
		if _, err := r.RunString(src); !errors.Is(err, ErrAllocationLimitExceeded) {
			t.Fatalf("%s: got %v, want the allocation limit", src, err)
		}
	}
	r.SetAllocationLimit(0)
	v, err := r.RunString(`var m; try { BigInt(si) } catch (e) { m = e.message } [m.length, m.charCodeAt(22), m.charCodeAt(23), m.slice(-14)].join()`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "2097187,12288,120,x) to a BigInt"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}

	skipHeavy(t)
	r = New()
	if _, err := r.RunString(`var s = Symbol("x".repeat(1 << 27));`); err != nil {
		t.Fatal(err)
	}
	r.SetAllocationLimit(1 << 20)
	start := time.Now()
	if _, err := r.RunString("BigInt(s)"); !errors.Is(err, ErrAllocationLimitExceeded) {
		t.Fatalf("got %v, want the allocation limit", err)
	}
	if took := time.Since(start); took > slack(50*time.Millisecond) {
		t.Fatalf("took %v", took)
	}
	r.SetAllocationLimit(0)
	interruptWithin(t, r, "BigInt(s)", 5*time.Millisecond, bigIntStepSlack)
}

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
	if raceDetectorEnabled {
		// Operands of 2^20 words: 0.5 s, 4.6 s under the race detector.
		t.Skip("slow under the race detector")
	}
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

// Trimming a string converted to a BigInt, of every kind of white space,
// gives Node v18.20.3's results, for a string made by the script or
// imported from Go (read in its UTF-8, without making its code units), and
// the message of one that is not a BigInt holds its first 1,000 code units.
func TestBigIntTrimAsNode(t *testing.T) {
	compareLines(t, runLogged(t, bigIntTrimScript), bigIntTrimNode)
	r := New()
	for _, s := range []string{" 12\u3000", "\ufeff-0x1f", "\u2028\u20290b101 ", "\u3000", "", "\u00851", "1\u180e", "\U0001F9EA1", strings.Repeat("\u3000", 600) + "x" + strings.Repeat("\U0001F9EA", 600)} {
		r.Set("s", s)
		v, err := r.RunString(`var made = s.split("").join(""); [made, s].map(t => { try { return String(BigInt(t)) } catch (e) { return e.message } }).concat(s == 31n, 1n < s)`)
		if err != nil {
			t.Fatal(err)
		}
		a := v.Export().([]interface{})
		if a[0] != a[1] {
			t.Errorf("%q: imported %q, made %q", s, a[1], a[0])
		}
		if _, ok := r.Get("s").(*importedString); !ok && len(s) > 16 {
			t.Errorf("%q: not imported", s)
		}
	}
}

// Trimming a string converted to a BigInt checks the interrupt between
// blocks of characters, as its conversion does, and so does reading a
// string imported from Go: Node's 1n written after 2^27 spaces, 2^26 ideographic
// spaces, or 2^27 imported spaces returned 66, 211 and 613 ms after an
// interrupt.
func TestBigIntTrimInterruptible(t *testing.T) {
	skipHeavy(t)
	r := New()
	r.Set("imported", strings.Repeat(" ", 1<<27)+"1")
	r.Set("importedUni", strings.Repeat("\u3000", 1<<26)+"1")
	if _, err := r.RunString(`var ascii = " ".repeat(2 ** 27) + "1", uni = "\u3000".repeat(2 ** 26) + "1", trail = "1" + "\u3000".repeat(2 ** 26);`); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"BigInt(ascii)", "BigInt(uni)", "BigInt(trail)", "BigInt(imported)", "BigInt(importedUni)", "imported == 1n"} {
		runtime.GC()
		interruptWithin(t, r, src, 5*time.Millisecond, slack(20*time.Millisecond))
	}
}

// A native frame is named after the function when its name property is a
// BigInt or an object, or is not a data property, as V8 names it: writing
// a BigInt name was unbounded, and running an object's toString or a
// getter while the allocation limit or an interrupt raised its error, which
// captures the stack, raised it again until the goroutine's stack
// overflowed (a fatal error).
func TestBigIntFunctionNameIntrinsic(t *testing.T) {
	r := New()
	v, err := runWithin(t, r, `var stacks = [];
	Object.defineProperty(Array.prototype.map, "name", {value: 123n});
	stacks.push([1].map(() => new Error().stack)[0]);
	Object.defineProperty(Array.prototype.filter, "name", {value: {toString() { throw new Error("ran") }}});
	stacks.push([1].filter(() => { stacks.push(new Error().stack) }));
	Object.defineProperty(Array.prototype.forEach, "name", {get() { throw new Error("ran") }});
	[1].forEach(() => stacks.push(new Error().stack));
	Object.defineProperty(BigInt, "name", {value: 5n});
	BigInt({valueOf() { stacks.push(new Error().stack); return 1 }});
	Object.defineProperty(Array.prototype.some, "name", {value: "renamed"});
	[1].some(() => stacks.push(new Error().stack));
	stacks.filter(s => typeof s === "string").map(s => s.split("\n")[2].trim()).join()`, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "at map (native),at filter (native),at forEach (native),at BigInt (native),at renamed (native)"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	r = New()
	if _, err := r.RunString(`var x = (1n << 30000000n) - 1n; Object.defineProperty(Array.prototype.map, "name", {value: {toString() { return x }}});`); err != nil {
		t.Fatal(err)
	}
	r.SetAllocationLimit(1 << 20)
	if _, err := runWithin(t, r, "[1].map(() => { var a = []; for (;;) a.push({}) })", 5*time.Second); !errors.Is(err, ErrAllocationLimitExceeded) {
		t.Fatalf("got %v, want the allocation limit", err)
	}
}

// Formatting a BigInt key a proxy's ownKeys gives in its TypeError goes
// through the runtime, charged and interruptible: a key of 4M bits made a
// message of 1.26 MB under a 1 MiB limit, charged 792 bytes, 320 ms after an
// interrupt. So does an object whose toString gives a BigInt, written in a
// message.
func TestBigIntOwnKeysMessageBounded(t *testing.T) {
	const setup = "var x = 1n << 4194304n, o = {toString() { return x }};"
	for _, src := range []string{
		"Reflect.ownKeys(new Proxy({}, {ownKeys() { return [x] } }))",
		"Object.keys(new Proxy({}, {ownKeys() { return [x] } }))",
		"new Date()[Symbol.toPrimitive](x)",
		"Object.setPrototypeOf({}, x)",
		"[{toLocaleString: 1, toString() { return x }}].toLocaleString()",
		"Number.prototype.valueOf.call(x)",
	} {
		r := New()
		if _, err := r.RunString(setup); err != nil {
			t.Fatal(err)
		}
		r.SetAllocationLimit(1 << 20)
		if _, err := runWithin(t, r, src, 5*time.Second); !errors.Is(err, ErrAllocationLimitExceeded) {
			t.Fatalf("%s: got %v, want the allocation limit", src, err)
		}
	}
	r := New()
	if _, err := r.RunString("var x = (1n << 30000000n) - 1n;"); err != nil {
		t.Fatal(err)
	}
	interruptWithin(t, r, "Reflect.ownKeys(new Proxy({}, {ownKeys() { return [x] } }))", 5*time.Millisecond, bigIntStepSlack)
}

const bigIntTrimScript = `const show = (f) => { try { const r = f(); return typeof r === 'bigint' ? r + 'n' : typeof r + ':' + String(r); } catch (e) { const m = e.message; return e.name + ': ' + m.length + ' ' + [m.charCodeAt(15), m.charCodeAt(16), m.charCodeAt(m.length - 13)].join(); } };
const ws = [" ", "\t", "\n", "\v", "\f", "\r", "\u00a0", "\u1680", "\u2000", "\u200a", "\u2028", "\u2029", "\u202f", "\u205f", "\u3000", "\ufeff"];
const cases = [];
for (const w of ws) cases.push(w + "12" + w, w + w + "-0x1f" + w, w + "0b101" + w + w);
cases.push("\u0085" + "1", "1" + "\u180e", "\u{1F9EA}1", " 1 2 ", "", "   ", "\u3000", "+7\u3000", "-\u30007", " \ud800 1");
for (const s of cases) console.log(JSON.stringify(s), '=>', show(() => BigInt(s)), show(() => s == 18n), show(() => 1n < s));
`

const bigIntTrimNode = "\" 12 \" => 12n boolean:false boolean:true\n\"  -0x1f \" => SyntaxError: 35 32,32,32 boolean:false boolean:false\n\" 0b101  \" => 5n boolean:false boolean:true\n\"\\t12\\t\" => 12n boolean:false boolean:true\n\"\\t\\t-0x1f\\t\" => SyntaxError: 35 9,9,9 boolean:false boolean:false\n\"\\t0b101\\t\\t\" => 5n boolean:false boolean:true\n\"\\n12\\n\" => 12n boolean:false boolean:true\n\"\\n\\n-0x1f\\n\" => SyntaxError: 35 10,10,10 boolean:false boolean:false\n\"\\n0b101\\n\\n\" => 5n boolean:false boolean:true\n\"\\u000b12\\u000b\" => 12n boolean:false boolean:true\n\"\\u000b\\u000b-0x1f\\u000b\" => SyntaxError: 35 11,11,11 boolean:false boolean:false\n\"\\u000b0b101\\u000b\\u000b\" => 5n boolean:false boolean:true\n\"\\f12\\f\" => 12n boolean:false boolean:true\n\"\\f\\f-0x1f\\f\" => SyntaxError: 35 12,12,12 boolean:false boolean:false\n\"\\f0b101\\f\\f\" => 5n boolean:false boolean:true\n\"\\r12\\r\" => 12n boolean:false boolean:true\n\"\\r\\r-0x1f\\r\" => SyntaxError: 35 13,13,13 boolean:false boolean:false\n\"\\r0b101\\r\\r\" => 5n boolean:false boolean:true\n\"\u00a012\u00a0\" => 12n boolean:false boolean:true\n\"\u00a0\u00a0-0x1f\u00a0\" => SyntaxError: 35 160,160,160 boolean:false boolean:false\n\"\u00a00b101\u00a0\u00a0\" => 5n boolean:false boolean:true\n\"\u168012\u1680\" => 12n boolean:false boolean:true\n\"\u1680\u1680-0x1f\u1680\" => SyntaxError: 35 5760,5760,5760 boolean:false boolean:false\n\"\u16800b101\u1680\u1680\" => 5n boolean:false boolean:true\n\"\u200012\u2000\" => 12n boolean:false boolean:true\n\"\u2000\u2000-0x1f\u2000\" => SyntaxError: 35 8192,8192,8192 boolean:false boolean:false\n\"\u20000b101\u2000\u2000\" => 5n boolean:false boolean:true\n\"\u200a12\u200a\" => 12n boolean:false boolean:true\n\"\u200a\u200a-0x1f\u200a\" => SyntaxError: 35 8202,8202,8202 boolean:false boolean:false\n\"\u200a0b101\u200a\u200a\" => 5n boolean:false boolean:true\n\"\u202812\u2028\" => 12n boolean:false boolean:true\n\"\u2028\u2028-0x1f\u2028\" => SyntaxError: 35 8232,8232,8232 boolean:false boolean:false\n\"\u20280b101\u2028\u2028\" => 5n boolean:false boolean:true\n\"\u202912\u2029\" => 12n boolean:false boolean:true\n\"\u2029\u2029-0x1f\u2029\" => SyntaxError: 35 8233,8233,8233 boolean:false boolean:false\n\"\u20290b101\u2029\u2029\" => 5n boolean:false boolean:true\n\"\u202f12\u202f\" => 12n boolean:false boolean:true\n\"\u202f\u202f-0x1f\u202f\" => SyntaxError: 35 8239,8239,8239 boolean:false boolean:false\n\"\u202f0b101\u202f\u202f\" => 5n boolean:false boolean:true\n\"\u205f12\u205f\" => 12n boolean:false boolean:true\n\"\u205f\u205f-0x1f\u205f\" => SyntaxError: 35 8287,8287,8287 boolean:false boolean:false\n\"\u205f0b101\u205f\u205f\" => 5n boolean:false boolean:true\n\"\u300012\u3000\" => 12n boolean:false boolean:true\n\"\u3000\u3000-0x1f\u3000\" => SyntaxError: 35 12288,12288,12288 boolean:false boolean:false\n\"\u30000b101\u3000\u3000\" => 5n boolean:false boolean:true\n\"\ufeff12\ufeff\" => 12n boolean:false boolean:true\n\"\ufeff\ufeff-0x1f\ufeff\" => SyntaxError: 35 65279,65279,65279 boolean:false boolean:false\n\"\ufeff0b101\ufeff\ufeff\" => 5n boolean:false boolean:true\n\"\u00851\" => SyntaxError: 29 133,49,49 boolean:false boolean:false\n\"1\u180e\" => SyntaxError: 29 49,6158,6158 boolean:false boolean:false\n\"\U0001f9ea1\" => SyntaxError: 30 55358,56810,49 boolean:false boolean:false\n\" 1 2 \" => SyntaxError: 32 32,49,32 boolean:false boolean:false\n\"\" => 0n boolean:false boolean:false\n\"   \" => 0n boolean:false boolean:false\n\"\u3000\" => 0n boolean:false boolean:false\n\"+7\u3000\" => 7n boolean:false boolean:true\n\"-\u30007\" => SyntaxError: 30 45,12288,55 boolean:false boolean:false\n\" \\ud800 1\" => SyntaxError: 31 32,55296,49 boolean:false boolean:false"
