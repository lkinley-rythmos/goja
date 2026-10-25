package goja

import (
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// BigInt(object) takes ToPrimitive with hint number, and converts a string
// it gives as StringToBigInt does, not through Number: Node v18.20.3's
// results (BigInt({valueOf: () => "9007199254740993"}) was 9007199254740992n).
func TestBigIntOfObjectAsNode(t *testing.T) {
	compareLines(t, runLogged(t, bigIntOfObjectScript), bigIntOfObjectNode)
}

const bigIntOfObjectScript = `const show = (f) => { try { const r = f(); return typeof r === 'bigint' ? r.toString() + 'n' : typeof r + ':' + String(r); } catch (e) { return e.name + ': ' + e.message; } };
const cases = [
  ['valueOf "9007199254740993"', () => BigInt({valueOf: () => "9007199254740993"})],
  ['valueOf "1.5"', () => BigInt({valueOf: () => "1.5"})],
  ['valueOf " 12 "', () => BigInt({valueOf: () => " 12 "})],
  ['valueOf "0x1f"', () => BigInt({valueOf: () => "0x1f"})],
  ['valueOf ""', () => BigInt({valueOf: () => ""})],
  ['valueOf "abc"', () => BigInt({valueOf: () => "abc"})],
  ['valueOf true', () => BigInt({valueOf: () => true})],
  ['valueOf 1.5', () => BigInt({valueOf: () => 1.5})],
  ['valueOf 7', () => BigInt({valueOf: () => 7})],
  ['valueOf 5n', () => BigInt({valueOf: () => 5n})],
  ['valueOf null', () => BigInt({valueOf: () => null})],
  ['valueOf undefined', () => BigInt({valueOf: () => undefined})],
  ['valueOf Symbol', () => BigInt({valueOf: () => Symbol("s")})],
  ['toString "77" only', () => BigInt({valueOf: undefined, toString: () => "77"})],
  ['toPrimitive hint', () => { let h; const r = BigInt({[Symbol.toPrimitive](hint) { h = hint; return "123456789012345678901234567890" }}); return h + ' ' + r; }],
  ['new String("42")', () => BigInt(new String("42"))],
  ['new Number(42)', () => BigInt(new Number(42))],
  ['new Boolean(true)', () => BigInt(new Boolean(true))],
  ['Object(5n)', () => BigInt(Object(5n))],
  ['[] ', () => BigInt([])],
  ['["12"]', () => BigInt(["12"])],
  ['[1,2]', () => BigInt([1, 2])],
  ['asUintN obj "300"', () => BigInt.asUintN(8, {valueOf: () => "300"})],
  ['asIntN obj 5', () => BigInt.asIntN(8, {valueOf: () => 5})],
  ['BigInt64Array obj "12"', () => { const a = new BigInt64Array(1); a[0] = {valueOf: () => "12"}; return a[0]; }],
];
for (const [k, f] of cases) console.log(k, '=>', show(f));
`

const bigIntOfObjectNode = `valueOf "9007199254740993" => 9007199254740993n
valueOf "1.5" => SyntaxError: Cannot convert 1.5 to a BigInt
valueOf " 12 " => 12n
valueOf "0x1f" => 31n
valueOf "" => 0n
valueOf "abc" => SyntaxError: Cannot convert abc to a BigInt
valueOf true => 1n
valueOf 1.5 => RangeError: The number 1.5 cannot be converted to a BigInt because it is not an integer
valueOf 7 => 7n
valueOf 5n => 5n
valueOf null => TypeError: Cannot convert null to a BigInt
valueOf undefined => TypeError: Cannot convert undefined to a BigInt
valueOf Symbol => TypeError: Cannot convert Symbol(s) to a BigInt
toString "77" only => 77n
toPrimitive hint => string:number 123456789012345678901234567890
new String("42") => 42n
new Number(42) => 42n
new Boolean(true) => 1n
Object(5n) => 5n
[]  => 0n
["12"] => 12n
[1,2] => SyntaxError: Cannot convert 1,2 to a BigInt
asUintN obj "300" => 44n
asIntN obj 5 => TypeError: Cannot convert 5 to a BigInt
BigInt64Array obj "12" => 12n`

// Every BigInt made for a script is charged, its digits and its header:
// zero and one results, conversions of booleans and numbers, typed-array
// and DataView reads, asIntN and asUintN of any width.
func TestBigIntEveryResultCharged(t *testing.T) {
	const n = 10000
	header := allocSizeBigInt
	for _, c := range []struct {
		src  string
		each int64 // bytes charged at least, each step
	}{
		{"BigInt.asUintN(1, true)", header},
		{"BigInt(true)", header},
		{"BigInt(7)", 8 + header},
		{"a / b", header},
		{"b >> 100n", header},
		{"nb >> 100n", 8 + header},
		{"a ** 0n", 8 + header},
		{"BigInt.asIntN(0, b)", header},
		{"BigInt.asUintN(0, b)", header},
		{"ta[0]", header},
		{"dv.getBigInt64(0)", 8 + header},
	} {
		t.Run(c.src, func(t *testing.T) {
			r := New()
			if _, err := r.RunString("var a = 1n, b = 2n, nb = -2n, ta = new BigUint64Array(1), dv = new DataView(new ArrayBuffer(8)); dv.setBigInt64(0, 5n);"); err != nil {
				t.Fatal(err)
			}
			r.SetAllocationLimit(0)
			if _, err := r.RunString("for (var i = 0; i < " + strconv.Itoa(n) + "; i++) " + c.src); err != nil {
				t.Fatal(err)
			}
			if got := r.AllocatedBytes(); got < n*c.each {
				t.Fatalf("charged %d bytes for %d, want at least %d", got, n, n*c.each)
			}
		})
	}
	// A zero result of asUintN is made as a zero, not from a copy of the low
	// words of x (16 of them took 16 MB, charged 712 bytes).
	r := New()
	if _, err := r.RunString("var n = 8000000, x = -(1n << BigInt(n));"); err != nil {
		t.Fatal(err)
	}
	r.SetAllocationLimit(1 << 20)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	v, err := r.RunString("var results = []; for (var i = 0; i < 16; i++) results.push(BigInt.asUintN(n, x)); results[0]")
	runtime.ReadMemStats(&after)
	if err != nil || v.String() != "0" {
		t.Fatalf("got %v, %v", v, err)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 4<<20 {
		t.Fatalf("allocated %d bytes", alloc)
	}
}

// No step of these operations on a BigInt near V8's limit goes on for more
// than about 50 ms without an interrupt check, and none copies the BigInt
// uncharged: a 64-bit typed-array store took 0.38 s (big.Int.Mod), asIntN
// 89 ms, x | y of negative numbers 131 ms, Map.set 130 ms (a copy of the
// bytes to hash), x % 1000003n 57 ms (a zeroed quotient), a comparison with
// a fraction copied all of x into a big.Float.
func TestBigIntStepsInterruptible(t *testing.T) {
	skipHeavy(t)
	const setup = "var x = (1n << 1073741760n) - 1n, y = x >> 1n, nx = -x, a = new BigInt64Array(1), m = new Map(), dv = new DataView(new ArrayBuffer(8));"
	r := New()
	if _, err := r.RunString(setup); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{
		"a[0] = x", "dv.setBigInt64(0, nx)", "BigInt.asIntN(1073741700, nx)", "BigInt.asUintN(1073741700, nx)",
		"BigInt.asIntN(1073741700, x)", "m.set(x, 1)", "nx & y", "nx | nx", "x ^ nx", "~x", "~nx", "x % 1000003n",
		"x < 1.5", "nx > -1.5", "1.5 < x",
	} {
		t.Run(src, func(t *testing.T) {
			prg := MustCompile("", src, false)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			timer := time.AfterFunc(5*time.Millisecond, func() { r.Interrupt("stop") })
			start := time.Now()
			_, err := r.RunProgram(prg)
			took := time.Since(start)
			timer.Stop()
			r.ClearInterrupt()
			runtime.ReadMemStats(&after)
			var ie *InterruptedError
			if err != nil && !errors.As(err, &ie) {
				t.Fatal(err)
			}
			if took > 5*time.Millisecond+bigIntStepSlack {
				t.Fatalf("returned after %v", took)
			}
			if strings.ContainsAny(src, "<>") || strings.HasPrefix(src, "a[") || strings.HasPrefix(src, "dv") {
				if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
					t.Fatalf("allocated %d bytes", alloc)
				}
			}
		})
	}
}
