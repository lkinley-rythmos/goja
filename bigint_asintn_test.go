package goja

import "testing"

// BigInt.asIntN and asUintN as Node v18.20.3 does them: ToIndex's
// "Invalid value: not (convertible to) a safe integer", before the BigInt
// is converted.
func TestBigIntAsIntNAsNode(t *testing.T) {
	compareLines(t, runLogged(t, asIntNScript), asIntNNode)
}

const asIntNScript = `const show = (f) => { try { const r = f(); return typeof r === 'bigint' ? r.toString() + 'n' : typeof r + ':' + String(r); } catch (e) { return e.name + ': ' + e.message; } };
const idx = [-1, -0.5, -0, 0.5, 1.5, NaN, Infinity, -Infinity, 2 ** 53 - 1, 2 ** 53, 2 ** 64, "-1", "x", undefined, null, true, {valueOf() { return 3 }}, 2 ** 31, -(2 ** 53)];
for (const n of idx) for (const fn of ["asUintN", "asIntN"]) console.log(fn + '(' + String(n) + ', 5n) =>', show(() => BigInt[fn](n, 5n)));
for (const fn of ["asUintN", "asIntN"]) {
  console.log(fn + '() =>', show(() => BigInt[fn]()));
  console.log(fn + '(-1) =>', show(() => BigInt[fn](-1)));
  console.log(fn + '(1) =>', show(() => BigInt[fn](1)));
  console.log(fn + '(Symbol()) =>', show(() => BigInt[fn](Symbol(), 1n)));
  console.log(fn + '(-1, Symbol()) =>', show(() => BigInt[fn](-1, Symbol())));
  console.log(fn + '(1, 5) =>', show(() => BigInt[fn](1, 5)));
  console.log(fn + '(1, "x") =>', show(() => BigInt[fn](1, "x")));
  console.log(fn + '(2**53, "x") =>', show(() => BigInt[fn](2 ** 53, "x")));
}
`

const asIntNNode = `asUintN(-1, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asIntN(-1, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asUintN(-0.5, 5n) => 0n
asIntN(-0.5, 5n) => 0n
asUintN(0, 5n) => 0n
asIntN(0, 5n) => 0n
asUintN(0.5, 5n) => 0n
asIntN(0.5, 5n) => 0n
asUintN(1.5, 5n) => 1n
asIntN(1.5, 5n) => -1n
asUintN(NaN, 5n) => 0n
asIntN(NaN, 5n) => 0n
asUintN(Infinity, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asIntN(Infinity, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asUintN(-Infinity, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asIntN(-Infinity, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asUintN(9007199254740991, 5n) => 5n
asIntN(9007199254740991, 5n) => 5n
asUintN(9007199254740992, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asIntN(9007199254740992, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asUintN(18446744073709552000, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asIntN(18446744073709552000, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asUintN(-1, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asIntN(-1, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asUintN(x, 5n) => 0n
asIntN(x, 5n) => 0n
asUintN(undefined, 5n) => 0n
asIntN(undefined, 5n) => 0n
asUintN(null, 5n) => 0n
asIntN(null, 5n) => 0n
asUintN(true, 5n) => 1n
asIntN(true, 5n) => -1n
asUintN([object Object], 5n) => 5n
asIntN([object Object], 5n) => -3n
asUintN(2147483648, 5n) => 5n
asIntN(2147483648, 5n) => 5n
asUintN(-9007199254740992, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asIntN(-9007199254740992, 5n) => RangeError: Invalid value: not (convertible to) a safe integer
asUintN() => TypeError: Cannot convert undefined to a BigInt
asUintN(-1) => RangeError: Invalid value: not (convertible to) a safe integer
asUintN(1) => TypeError: Cannot convert undefined to a BigInt
asUintN(Symbol()) => TypeError: Cannot convert a Symbol value to a number
asUintN(-1, Symbol()) => RangeError: Invalid value: not (convertible to) a safe integer
asUintN(1, 5) => TypeError: Cannot convert 5 to a BigInt
asUintN(1, "x") => SyntaxError: Cannot convert x to a BigInt
asUintN(2**53, "x") => RangeError: Invalid value: not (convertible to) a safe integer
asIntN() => TypeError: Cannot convert undefined to a BigInt
asIntN(-1) => RangeError: Invalid value: not (convertible to) a safe integer
asIntN(1) => TypeError: Cannot convert undefined to a BigInt
asIntN(Symbol()) => TypeError: Cannot convert a Symbol value to a number
asIntN(-1, Symbol()) => RangeError: Invalid value: not (convertible to) a safe integer
asIntN(1, 5) => TypeError: Cannot convert 5 to a BigInt
asIntN(1, "x") => SyntaxError: Cannot convert x to a BigInt
asIntN(2**53, "x") => RangeError: Invalid value: not (convertible to) a safe integer`
