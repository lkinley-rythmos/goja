# Allocation Shape Batch 5 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Halve the heap-object count of every array, plain object, closure, descriptor and array iterator the engine creates, give object literals their final map/key-list capacity up front, and stop the string/regexp builtins from re-boxing values that already are strings.

**Architecture:** Per-type "box" structs (`{ impl; obj Object }`) let the hot constructors allocate the public `Object` and its `objectImpl` in one allocation, with `impl.val = &box.obj` and `box.obj.self = &box.impl`; nothing else about `Object` or `objectImpl` changes. The compiler counts an object literal's statically-keyed properties and emits `newObject(n)`, which creates the object through `newObjectSized` (shared with `NewObjectWithCapacity`). A `toStringValue(v Value) String` helper returns a value that already is a `String` as is, and replaces `.toString()` at the `Value`-typed coercion sites in `builtin_string.go` and `builtin_regexp.go`.

**Tech Stack:** Go 1.25 (module floor), `testing.AllocsPerRun` for allocation acceptance, `testing` benchmarks with `-benchmem`, `benchstat` (`~/go/bin/benchstat`), tc39 test262 under `testdata/test262`.

**Spec:** `docs/superpowers/specs/2026-09-17-allocation-shape-batch5-design.md`

## Global Constraints

- Go floor `go 1.25.0` (from `go.mod`).
- All work on branch `perf/allocation-shape-batch5` (exists; spec committed at `e41cf0f`, branched from `master` at `3ebdf77`).
- Every commit must pass: `gofmt -l .` (empty), `go vet ./...`, `go test ./...` with test262 checked out (`testdata/test262` present; Task 0 verifies), `go test -race` on the tests the task adds, and — for Tasks 3, 4 and 5 — `GOARCH=386 go test -run '<the task's tests>' .` (the box structs change memory layout; CI has a 386 leg).
- Tests and benchmarks go in goja's own `*_test.go` files (package `goja`). Never commit anything under `testdata/test262`.
- No public API is added, removed or changed. No new dependencies. `Object`'s fields and the `objectImpl` interface do not change.
- Commit messages end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` — exactly, regardless of the implementer's model. Angle-bracket placeholders in commit-message templates (`<before>`, `<after>`, `<benchstat output>`) are filled with the measured lines from that task's benchmark step, never left as is.
- Characterization tests (Tasks 1, 2, 3): the golden expected strings below were captured from the unmodified engine at `e41cf0f` (engine identical to `master` `3ebdf77`). Each such test MUST pass before the task's engine change and after it; the implementer runs it both times and records both runs. A golden mismatch after the change is a defect in the change, never a reason to edit the expected string.
- Allocation acceptance uses `testing.AllocsPerRun` with exact expected counts (zero slack). "Before" counts below were measured on `e41cf0f`; "after" counts marked *derived* were computed from the design, not measured — if a GREEN run disagrees with a derived count, the implementer reports the measured number and stops for a ruling rather than adjusting the test to whatever it got.
- Every constructor this plan boxes must satisfy, immediately after construction: `impl.val == obj` and `obj.self == impl` (Task 3's wiring test states this for each type).

---

### Task 0: Prerequisites

**Files:** none modified.

- [ ] **Step 1: Confirm branch, clean tree, test262**

Run:
```bash
cd /home/lk/Work/goja && git branch --show-current && git status --porcelain | wc -l && ls testdata/test262/test | head -2
```
Expected: `perf/allocation-shape-batch5`, `0`, a directory listing. If `testdata/test262` is missing run `./.tc39_test262_checkout.sh`.

- [ ] **Step 2: Green base**

Run:
```bash
cd /home/lk/Work/goja && gofmt -l . && go vet ./... && go test ./... 2>&1 | tail -5
```
Expected: no gofmt output, no vet output, every package `ok` (the root package includes test262 and takes a few minutes).

- [ ] **Step 3: Record the baseline commit**

Run: `git rev-parse HEAD` — expected `e41cf0f…`. Write it in the task report; Task 6 diffs against it.

---

### Task 1: `toStringValue` and the string/regexp coercion sweep (spec §5)

**Files:**
- Modify: `runtime.go` (helper next to `toPropertyKey`, ~line 2698)
- Modify: `builtin_string.go` (49 sites), `builtin_regexp.go` (25 sites)
- Create: `string_coerce_test.go`

**Interfaces:**
- Consumes: `String` interface, `Value.toString() String`.
- Produces: `func toStringValue(v Value) String`.

- [ ] **Step 1: Write the characterization test and run it on the unmodified engine**

Create `string_coerce_test.go`:

```go
package goja

import "testing"

// coerceGoldenScript exercises every string/regexp builtin that coerces
// its receiver or an argument to a string, with receivers/arguments that
// already are strings, objects with toString/valueOf, primitives,
// null/undefined, Symbols (TypeError text), non-ASCII strings, and the
// empty-string edge cases.
const coerceGoldenScript = `
var out = [];
var s = 'hello world hello';
var objS = {toString: function() { return 'wor' }}; var objV = {valueOf: function() { return 42 }, toString: function() { return 'v' }};
out.push(s.indexOf('hello'), s.indexOf('hello', 1), s.indexOf(objS), s.indexOf(objV), s.indexOf(), s.indexOf(undefined), s.indexOf(null), s.indexOf(1), s.lastIndexOf('hello'), s.lastIndexOf(objS), s.includes('world'), s.includes(objS), s.startsWith('hel'), s.startsWith(objS, 6), s.endsWith('llo'), s.endsWith(objS, 9));
out.push(s.split(' ').join('|'), s.split(objS).length, s.replace('hello', 'bye'), s.replace(objS, objV), s.replaceAll('hello', 'x'), s.search('wor'), s.search(objS), JSON.stringify(s.match('l+')), s.match(objS)[0], s.concat(objS, 1, null), s.padStart(20, objS), s.repeat(2).length, s.at(-1), s.charAt(objV), s.substring(objV), s.slice(objS), s.localeCompare(objS), s.normalize(), s.trim(), s.toUpperCase(), s.codePointAt(objV), s.charCodeAt(1));
out.push(/wor/.test(s), /wor/.test(objS), /wor/.test(objV), /o/g.exec(s).index, /o/.exec(objS)[0], String(/x/.exec(null)), /nul/.test(null), /undef/.test(undefined), /42/.test(objV), new RegExp(objS).source, new RegExp('a', 'g').flags, 'aXbXc'.split(/X/).join('-'), 'abc'.replace(/b/, objS), 'abc'.replace(/b/, function(m) { return m.toUpperCase() }), s.match(/l/g).length, [...s.matchAll(/l/g)].length, s.search(/x/));
var nonStr = [123, true, null, undefined, {a: 1}, [1, 2], 1.5];
for (var i = 0; i < nonStr.length; i++) { try { out.push(String.prototype.indexOf.call(nonStr[i], '1') + ':' + String.prototype.slice.call(nonStr[i], 0, 2) + ':' + String.prototype.split.call(nonStr[i], ',').length) } catch (e) { out.push(e.constructor.name + ':' + e.message) } }
try { s.indexOf(Symbol('x')) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { /x/.test(Symbol('x')) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { String.prototype.indexOf.call(Symbol('x'), 'x') } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { s.split(Symbol('x')) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
var u = 'héllo wörld'; out.push(u.indexOf('wör'), u.indexOf({toString: function() { return 'ö' }}), u.split('ö').join('|'), u.replace('é', 'e'), /ö/.test(u), u.lastIndexOf('l'), u.includes('llo w'), u.startsWith('hé'), u.endsWith('rld'));
out.push(''.indexOf(''), 'abc'.indexOf('', 5), 'abc'.lastIndexOf('', 1), 'aaa'.split('').length, 'a,b'.split(',', 1).join(''), 'x'.padEnd(3, objS), String.raw({raw: ['a', 'b']}, objS), 'abc'.startsWith(''), 'abc'.endsWith('c', 3));
out.join('|');
`

// coerceGolden was captured from the unmodified engine at e41cf0f.
const coerceGolden = `0|12|6|-1|-1|-1|-1|-1|12|6|true|true|true|true|true|true|hello|world|hello|2|bye world hello|hello vld hello|x world x|6|6|["ll"]|wor|hello world hellowor1null|worhello world hello|34|o|||hello world hello|-1|hello world hello|hello world hello|HELLO WORLD HELLO||101|true|true|false|4|o|null|true|true|false|wor|g|a-b-c|aworc|aBc|5|5|-1|0:12:1|-1:tr:1|TypeError:Value is not object coercible|TypeError:Value is not object coercible|-1:[o:1|0:1,:2|0:1.:1|TypeError:Cannot convert a Symbol value to a string|TypeError:Cannot convert a Symbol value to a string|TypeError:Cannot convert a Symbol value to a string|TypeError:Cannot convert a Symbol value to a string|6|7|héllo w|rld|hello wörld|true|9|true|true|true|0|3|1|3|a|xwo|aworb|true|true`

func TestStringCoercionCharacterization(t *testing.T) {
	testScript(coerceGoldenScript, asciiString(coerceGolden), t)
}
```

Note: the script's `\|` inside `split(' ').join('|')` is a plain `|` in the JS — the golden separators are the same character, which is why the `split` result appears as three segments (`hello|world|hello`) in the golden; that is expected.

Run: `go test -run TestStringCoercionCharacterization -count=1 -v .`
Expected: `--- PASS` on the unmodified engine. If it fails here, the golden or script was mis-transcribed — compare against the plan text; do not proceed until it passes unmodified.

- [ ] **Step 2: Write the failing allocation test**

Append to `string_coerce_test.go`:

```go
// measureProgramAllocs returns the allocations of running src once in vm,
// minus the fixed cost of RunProgram itself (measured on the constant 1).
func measureProgramAllocs(t *testing.T, vm *Runtime, src string) float64 {
	t.Helper()
	run := func(src string) float64 {
		prg, err := Compile("m", src, false)
		if err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(100, func() {
			if _, err := vm.RunProgram(prg); err != nil {
				panic(err)
			}
		})
	}
	return run(src) - run("1")
}

// TestStringCoercionNoRebox: coercing a value that already is a String
// must not allocate. indexOf coerces this and the argument (2 boxes);
// split coerces this and the separator (2); RegExp.prototype.test
// coerces the argument (1). Counts measured on e41cf0f before / after.
func TestStringCoercionNoRebox(t *testing.T) {
	vm := New()
	if _, err := vm.RunString(`var str = 'hello world'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		src  string
		want float64
	}{
		{"str.indexOf('l')", 1},
		{"str.indexOf('l', 1)", 1},
		{"str.split('l')", 8},
		{"/l/.test(str)", 8},
	} {
		if got := measureProgramAllocs(t, vm, tc.src); got != tc.want {
			t.Errorf("%s: %v allocations, want %v", tc.src, got, tc.want)
		}
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test -run TestStringCoercionNoRebox -count=1 -v .`
Expected: FAIL with `str.indexOf('l'): 3 allocations, want 1`, `str.indexOf('l', 1): 3`, `str.split('l'): 10`, `/l/.test(str): 9` (measured at `e41cf0f`). The `want` values are *derived* (RED minus the number of `Value`-typed `toString()` coercions on that path); if GREEN measures something else, report it and stop.

- [ ] **Step 4: Add the helper**

In `runtime.go`, directly after `toPropertyKey`:

```go
// toStringValue coerces v to a String. A value that already is one is
// returned as is: calling toString on it would re-box the same string
// into a fresh interface value on every call.
func toStringValue(v Value) String {
	if s, ok := v.(String); ok {
		return s
	}
	return v.toString()
}
```

- [ ] **Step 5: Sweep the coercion sites**

In `builtin_string.go` and `builtin_regexp.go`, replace every `X.toString()` whose receiver `X` has static type `Value` with `toStringValue(X)`. Concretely (line numbers as of `e41cf0f`):

- `builtin_string.go`: 34, 64, 105, 161, 167, 175, 189, 199, 209, 232, 244, 284, 289, 313, 318, 334, 335, 352, 353, 376, 377, 405, 419, 436, 445, 448, 484, 495, 554, 592, 635, 695, 697, 715, 727, 729, 767, 776, 827, 846, 933, 938, 959, 990, 997, 1004, 1012, 1020, 1028 (49 sites).
- `builtin_regexp.go`: 402, 405, 407, 409, 417, 420, 466, 471, 489, 498, 537, 538, 786, 841, 889, 890, 958, 973, 1087, 1093, 1202, 1228, 1244, 1251, 1343 (25 sites).

Patterns: `call.This.toString()` → `toStringValue(call.This)`; `call.Argument(N).toString()` → `toStringValue(call.Argument(N))`; `X.toString().String()` → `toStringValue(X).String()`; `nilSafe(expr).toString()` → `toStringValue(nilSafe(expr))`; a chained `}).toString()` on a call returning `Value` → wrap the whole call: `toStringValue(r.call(...))`; line 448's `formArg.toString().toString().String()` → `toStringValue(formArg).String()`. Every receiver on the list is a `Value` (`call.This`, `call.Argument(n)`, `call.Arguments[i]`, `Value` locals such as `arg`, `args[0]`, `searchString`, `searchValue`, `fillString`, `replaceValue`, `separatorValue`, `formArg`, `patternVal`, `flagsVal`, `flags`, `capture`, the results of `nilSafe(...)` and of `getStr`/`getIdx`); if you find one that is not, leave it and note it in the report. Confirm afterwards:

```bash
grep -c "toStringValue(" builtin_string.go builtin_regexp.go   # expected 49 and 25
grep -n "\.toString()" builtin_string.go builtin_regexp.go      # expected: no output
```

- [ ] **Step 6: Run the tests**

Run: `gofmt -l . ; go vet . && go test -run 'TestStringCoercion|TestString|TestRegexp|TestRegExp' -count=1 . && go test -race -run 'TestStringCoercion' .`
Expected: all `ok`; the golden passes unchanged after the change (record both runs). If `TestStringCoercionNoRebox` measures other than the `want` values, report the numbers and stop for a ruling.

- [ ] **Step 7: Commit**

```bash
git add runtime.go builtin_string.go builtin_regexp.go string_coerce_test.go
git commit -m "Coerce to String without re-boxing values that already are strings

Value.toString() on an asciiString/unicodeString/*importedString returns
the receiver, but returning it through the String interface allocates a
16-byte box per call. toStringValue returns such values as is; the
string and regexp builtins use it at every Value-typed coercion site.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: Pre-sized object literals (spec §4)

**Files:**
- Modify: `compiler_expr.go` (`compiledObjectLiteral.emitGetter`, ~line 2840)
- Modify: `vm.go:2743-2750` (`newObject` opcode)
- Modify: `vm_test.go:29` (an opcode list that names `newObject`)
- Modify: `runtime.go:538-545` (`NewObjectWithCapacity` → `newObjectSized`)
- Create: `object_literal_test.go`

**Interfaces:**
- Consumes: `ast.PropertyKeyed{Key, Kind, Value, Computed}`, `ast.PropertyShort`, `ast.SpreadElement`, `(*compiler).emit`, `(*Runtime).newBaseObject`, `baseObject.values/propNames`.
- Produces: `type newObject uint32` (opcode; `newObject(n).exec`), `func (r *Runtime) newObjectSized(n int) *Object`.

- [ ] **Step 1: Write the characterization test and run it on the unmodified engine**

Create `object_literal_test.go`:

```go
package goja

import "testing"

// literalGoldenScript pins the key order, values and enumeration of
// object literals across every property form the compiler counts or
// skips: keyed values, shorthand, computed keys, methods, getter/setter
// pairs, __proto__, spread (before/after/between), duplicate keys, more
// than eight keys, numeric-string keys, nesting, symbol keys and
// function-name inference.
const literalGoldenScript = `
var out = [];
function show(o) { var ks = []; for (var k in o) ks.push(k); return Object.keys(o).join(',') + '/' + JSON.stringify(o) + '/' + Object.getOwnPropertyNames(o).join(',') + '/' + ks.join(','); }
var g = 1;
var c = 'comp';
out.push(show({}), show({a: 1}), show({a: 1, b: 2, c: 3}), show({b: 2, a: 1, 1: 'one', 0: 'zero'}), show({a: 1, a: 2}), show({g, c}), show({[c]: 1, x: 2}), show({x: 1, [c + '2']: 2, y: 3}), show({m() { return 1 }, get q() { return 2 }, set q(v) {}, z: 3}), show({a: 1, ...{b: 2, c: 3}, d: 4}), show({...{a: 1}, a: 2}), show({__proto__: {inh: 1}, own: 2}), show({__proto__: null, x: 1}));
var big = {k1: 1, k2: 2, k3: 3, k4: 4, k5: 5, k6: 6, k7: 7, k8: 8, k9: 9, k10: 10, k11: 11, k12: 12}; out.push(show(big));
var nested = {a: {b: {c: [1, {d: 2}]}}, e: 3}; out.push(show(nested), show(nested.a), show(nested.a.b.c[1]));
var p = {__proto__: {inh: 1}, own: 2}; out.push(p.inh + '/' + Object.getPrototypeOf(p).inh + '/' + ('inh' in p) + '/' + p.hasOwnProperty('inh'));
var n = {x: 1}; n.y = 2; delete n.x; n.x = 3; out.push(show(n));
var desc = Object.getOwnPropertyDescriptors({a: 1, get b() { return 2 }, m() {}}); out.push(Object.keys(desc).join(',') + '/' + JSON.stringify(desc.a) + '/' + typeof desc.b.get + '/' + desc.m.enumerable);
var sym = Symbol('s'); var ws = {[sym]: 1, a: 2}; out.push(show(ws) + '/' + Object.getOwnPropertySymbols(ws).length);
function mk(i) { return {i: i, sq: i * i} } var arr = []; for (var i = 0; i < 20; i++) arr.push(mk(i)); out.push(JSON.stringify(arr.map(function(o) { return o.sq })));
var withMethodName = {f: function() {}, ar: () => 1, [c]: function() {}}; out.push(withMethodName.f.name + '/' + withMethodName.ar.name + '/' + withMethodName.comp.name);
out.push(Object.isExtensible({}), Object.getPrototypeOf({}) === Object.prototype, Object.prototype.toString.call({a: 1}));
out.join('|');
`

// literalGolden was captured from the unmodified engine at e41cf0f.
const literalGolden = `/{}//|a/{"a":1}/a/a|a,b,c/{"a":1,"b":2,"c":3}/a,b,c/a,b,c|0,1,b,a/{"0":"zero","1":"one","b":2,"a":1}/0,1,b,a/0,1,b,a|a/{"a":2}/a/a|g,c/{"g":1,"c":"comp"}/g,c/g,c|comp,x/{"comp":1,"x":2}/comp,x/comp,x|x,comp2,y/{"x":1,"comp2":2,"y":3}/x,comp2,y/x,comp2,y|m,q,z/{"q":2,"z":3}/m,q,z/m,q,z|a,b,c,d/{"a":1,"b":2,"c":3,"d":4}/a,b,c,d/a,b,c,d|a/{"a":2}/a/a|own/{"own":2}/own/own,inh|x/{"x":1}/x/x|k1,k2,k3,k4,k5,k6,k7,k8,k9,k10,k11,k12/{"k1":1,"k2":2,"k3":3,"k4":4,"k5":5,"k6":6,"k7":7,"k8":8,"k9":9,"k10":10,"k11":11,"k12":12}/k1,k2,k3,k4,k5,k6,k7,k8,k9,k10,k11,k12/k1,k2,k3,k4,k5,k6,k7,k8,k9,k10,k11,k12|a,e/{"a":{"b":{"c":[1,{"d":2}]}},"e":3}/a,e/a,e|b/{"b":{"c":[1,{"d":2}]}}/b/b|d/{"d":2}/d/d|1/1/true/false|y,x/{"y":2,"x":3}/y,x/y,x|a,b,m/{"value":1,"writable":true,"enumerable":true,"configurable":true}/function/true|a/{"a":2}/a/a/1|[0,1,4,9,16,25,36,49,64,81,100,121,144,169,196,225,256,289,324,361]|f/ar/comp|true|true|[object Object]`

func TestObjectLiteralCharacterization(t *testing.T) {
	testScript(literalGoldenScript, asciiString(literalGolden), t)
}
```

Run: `go test -run TestObjectLiteralCharacterization -count=1 -v .`
Expected: `--- PASS` on the unmodified engine. If it fails here, the golden or script was mis-transcribed — compare against the plan text; do not proceed until it passes unmodified.

- [ ] **Step 2: Write the failing allocation test**

Append to `object_literal_test.go` (it reuses `measureProgramAllocs` from Task 1's `string_coerce_test.go`):

```go
// TestObjectLiteralPresized: a literal with k statically-known keys is
// created with room for k, so initialising it neither grows the map nor
// the key list. Before (e41cf0f): {} = 2 (Object + baseObject);
// {a,b,c} = 7 (2 + map header + first group + 3 propNames growths);
// ten keys = 12. After (derived): {} unchanged; {a,b,c} = 5 (2 + header
// + first group on the first insert + one propNames allocation); ten
// keys = 6 (2 + the 3 allocations Go makes for a map hinted above 8 +
// one propNames allocation).
func TestObjectLiteralPresized(t *testing.T) {
	vm := New()
	for _, tc := range []struct {
		src  string
		want float64
	}{
		{"({})", 2},
		{"({a: 1, b: 2, c: 3})", 5},
		{"({a: 1, b: 2, c: 3, d: 4, e: 5, f: 6, g: 7, h: 8, i: 9, j: 10})", 6},
	} {
		if got := measureProgramAllocs(t, vm, tc.src); got != tc.want {
			t.Errorf("%s: %v allocations, want %v", tc.src, got, tc.want)
		}
	}
	// The compiler's count: computed keys and spreads are not counted,
	// __proto__ and shorthand are; getter+setter on one key counts twice.
	// Verified through the opcode the compiler emits.
	for _, tc := range []struct {
		src  string
		want uint32
	}{
		{"({})", 0},
		{"({a: 1, b: 2})", 2},
		{"({a: 1, [k]: 2, b: 3})", 2},
		{"({a, b})", 2},
		{"({...o, a: 1})", 1},
		{"({__proto__: p, a: 1})", 2},
		{"({get x() {}, set x(v) {}, m() {}})", 3},
	} {
		prg, err := Compile("c", tc.src, false)
		if err != nil {
			t.Fatal(err)
		}
		var got uint32
		found := false
		for _, ins := range prg.code {
			if n, ok := ins.(newObject); ok {
				got, found = uint32(n), true
				break
			}
		}
		if !found || got != tc.want {
			t.Errorf("%s: newObject count = %d (found %v), want %d", tc.src, got, found, tc.want)
		}
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test -run TestObjectLiteralPresized -count=1 -v .`
Expected: compile error `newObject (type _newObject) is not an expression` / cannot convert (the opcode is still the struct singleton), or — once the type exists — `({a: 1, b: 2, c: 3}): 7 allocations, want 5` and `…j: 10}): 12 allocations, want 6`.

- [ ] **Step 4: Make the opcode carry a count**

In `vm.go` replace:

```go
type _newObject struct{}

var newObject _newObject

func (_newObject) exec(vm *vm) {
	vm.push(vm.r.NewObject())
	vm.pc++
}
```

with:

```go
// newObject creates an ordinary object with room for the given number of
// properties (the object literal's statically-keyed count); 0 keeps the
// storage lazy.
type newObject uint32

func (n newObject) exec(vm *vm) {
	vm.push(vm.r.newObjectSized(int(n)))
	vm.pc++
}
```

In `vm_test.go:29`, the opcode list entry `newObject,` becomes `newObject(0),`.

- [ ] **Step 5: Share the sized constructor**

In `runtime.go` replace `NewObjectWithCapacity` with:

```go
// newObjectSized creates an ordinary object whose property storage is
// pre-sized for n string-keyed properties; n <= 0 leaves the storage lazy
// exactly as NewObject does.
func (r *Runtime) newObjectSized(n int) *Object {
	o := r.newBaseObject(r.global.ObjectPrototype, classObject)
	if n > 0 {
		o.values = make(map[unistring.String]Value, n)
		o.propNames = make([]unistring.String, 0, n)
	}
	return o.val
}

// NewObjectWithCapacity creates an ordinary object (as NewObject) whose
// property storage is pre-sized for n string-keyed properties, so that
// defining up to n properties causes no map or key-list growth.
// n <= 0 behaves exactly like NewObject.
func (r *Runtime) NewObjectWithCapacity(n int) *Object {
	return r.newObjectSized(n)
}
```

- [ ] **Step 6: Count in the compiler**

In `compiler_expr.go`, change the first two lines of `compiledObjectLiteral.emitGetter` from:

```go
	e.addSrcMap()
	e.c.emit(newObject)
```

to:

```go
	e.addSrcMap()
	e.c.emit(newObject(e.staticPropCount()))
```

and add, directly above `emitGetter`:

```go
// staticPropCount returns the number of properties of the literal whose
// key is known at compile time. The object is created with room for that
// many, so initialising it grows neither its map nor its key list.
// Computed keys and spreads are unknown and not counted; __proto__ adds
// no key but is counted anyway (one spare slot is harmless); a getter and
// setter on the same key count twice for the same reason.
func (e *compiledObjectLiteral) staticPropCount() uint32 {
	var n uint32
	for _, prop := range e.expr.Value {
		switch prop := prop.(type) {
		case *ast.PropertyKeyed:
			if !prop.Computed {
				n++
			}
		case *ast.PropertyShort:
			n++
		}
	}
	return n
}
```

- [ ] **Step 7: Run the tests**

Run: `gofmt -l . ; go vet . && go test -run 'TestObjectLiteral|TestNewObjectWithCapacity|TestCompile|TestVM|TestObject' -count=1 . && go test -race -run 'TestObjectLiteral' .`
Expected: all `ok`; `TestObjectLiteralCharacterization` unchanged after the change (record both runs). If `TestObjectLiteralPresized` measures other than 5 / 6, report the numbers and stop for a ruling — do not edit the expectations.

- [ ] **Step 8: Run test262**

Run: `go test -run TestTC39 -count=1 . 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 9: Commit**

```bash
git add compiler_expr.go vm.go vm_test.go runtime.go object_literal_test.go
git commit -m "Create object literals with room for their statically-keyed properties

The compiler counts the keyed and shorthand properties of a literal and
emits newObject(n); the object is created through newObjectSized (now
shared with NewObjectWithCapacity), so putProp grows neither the map nor
the key list. Computed keys and spreads are not counted.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: One allocation per plain object (spec §3, `newBaseObject`)

**Files:**
- Modify: `runtime.go:516-519` (`newBaseObject`)
- Create: `object_box_test.go`

**Interfaces:**
- Consumes: `newBaseObjectObj(obj, proto *Object, class string) *baseObject` (unchanged), `baseObject`, `Object`.
- Produces: `type baseObjectBox struct { baseObject; obj Object }`; the box pattern Tasks 4 and 5 repeat for their types.

- [ ] **Step 1: Write the characterization test and run it on the unmodified engine**

Create `object_box_test.go`:

```go
package goja

import "testing"

// coallocGoldenScript pins behaviour that depends on an object's impl and
// its *Object staying consistent across representation changes: dense
// to sparse arrays and back, generators, arrays as prototypes, prototype
// chains, functions/arrows/methods and their own properties, descriptor
// objects, array iterators, for-of, JSON, classes, and the array builtins
// that create arrays.
const coallocGoldenScript = `
var out = [];
var a = [1, 2, 3]; a[1000000] = 'far'; out.push(a.length + '/' + a[1000000] + '/' + a[1] + '/' + Object.keys(a).join(',') + '/' + a.indexOf('far'));
a.length = 3; out.push(a.length + '/' + JSON.stringify(a) + '/' + Object.keys(a).join(','));
delete a[1]; out.push(JSON.stringify(a) + '/' + (1 in a) + '/' + a.length);
var b = []; b[10] = 1; b.length = 0; b.push(5); out.push(JSON.stringify(b));
function* gen() { yield 1; yield 2 } var g = gen(); out.push(g.next().value + '/' + g.next().value + '/' + g.next().done + '/' + Object.prototype.toString.call(g));
var protoArr = [1, 2]; var child = Object.create(protoArr); child.push(3); out.push(child.length + '/' + JSON.stringify(protoArr) + '/' + Array.isArray(child) + '/' + child[0]);
var chain = Object.create(Object.create({deep: 1})); out.push(chain.deep + '/' + Object.getPrototypeOf(Object.getPrototypeOf(chain)).deep);
var f = function(x) { return x * 2 }; var arrow = (x) => x + 1; var obj = { m(x) { return x - 1 } };
out.push(f(2) + '/' + arrow(2) + '/' + obj.m(2) + '/' + typeof f + '/' + f.name + '/' + arrow.name + '/' + obj.m.name + '/' + f.length + '/' + (f instanceof Function) + '/' + Object.prototype.toString.call(arrow) + '/' + (f.prototype.constructor === f));
f.custom = 1; arrow.custom = 2; out.push(f.custom + '/' + arrow.custom + '/' + Object.keys(f).join(',') + '/' + ('prototype' in arrow) + '/' + ('prototype' in obj.m));
var d = Object.getOwnPropertyDescriptor({q: 1}, 'q'); d.extra = 1; out.push(JSON.stringify(d) + '/' + Object.keys(d).join(','));
var it = [1, 2][Symbol.iterator](); out.push(it.next().value + '/' + it.next().value + '/' + it.next().done + '/' + Object.prototype.toString.call(it) + '/' + (it[Symbol.iterator]() === it));
var seen = []; for (var v of [7, 8]) seen.push(v); for (var [k, w] of [['a', 1]]) seen.push(k + w); for (var e of [9].entries()) seen.push(e.join(':')); out.push(seen.join(','));
out.push(JSON.stringify({a: [1, {b: 2}], f: function() {}, u: undefined}), JSON.stringify([1, , 3]), JSON.parse('{"x":[1,2,{"y":3}]}').x[2].y);
var cls = class K { constructor() { this.v = 1 } }; out.push(new cls().v + '/' + (new cls() instanceof cls));
out.push([1, 2, 3].map(function(x) { return x * 2 }).filter(function(x) { return x > 2 }).join(','), Array.from({length: 3}, function(_, i) { return i }).join(','), [3, 1, 2].sort().join(','), [1, 2, 3].slice(1).concat([4]).join(','), Array.isArray(new Array(2)), new Array(2).length);
var ctor = function C() { this.x = 1 }; ctor.prototype.m = function() { return this.x }; var inst = new ctor(); out.push(inst.m() + '/' + (Object.getPrototypeOf(inst) === ctor.prototype) + '/' + inst.constructor.name);
var fr = Object.freeze([1, 2]); try { 'use strict'; (function() { 'use strict'; fr.push(3) })(); out.push('nothrow') } catch (e) { out.push(e.constructor.name) }
out.push(Object.isFrozen(fr) + '/' + Object.isFrozen(Object.freeze({a: 1})) + '/' + Object.isSealed(Object.seal(function() {})));
out.join('|');
`

// coallocGolden was captured from the unmodified engine at e41cf0f.
const coallocGolden = `1000001/far/2/0,1,2,1000000/1000000|3/[1,2,3]/0,1,2|[1,null,3]/false/3|[5]|1/2/true/[object Generator]|3/[1,2]/false/1|1/1|4/3/1/function/f/arrow/m/1/true/[object Function]/true|1/2/custom/false/false|{"value":1,"writable":true,"enumerable":true,"configurable":true,"extra":1}/value,writable,enumerable,configurable,extra|1/2/true/[object Array Iterator]/true|7,8,a1,0:9|{"a":[1,{"b":2}]}|[1,null,3]|3|1/true|4,6|0,1,2|1,2,3|2,3,4|true|2|1/true/C|TypeError|true/true/true`

func TestCoallocCharacterization(t *testing.T) {
	testScript(coallocGoldenScript, asciiString(coallocGolden), t)
}
```

Run: `go test -run TestCoallocCharacterization -count=1 -v .`
Expected: `--- PASS` on the unmodified engine. If it fails here, the golden or script was mis-transcribed — compare against the plan text; do not proceed until it passes unmodified.

- [ ] **Step 2: Write the failing wiring and allocation tests**

Append to `object_box_test.go`:

```go
// checkWiring asserts the invariant every boxed constructor must satisfy:
// the impl's val is the Object and the Object's self is the impl.
func checkWiring(t *testing.T, what string, obj *Object, impl objectImpl) {
	t.Helper()
	if obj.self != impl {
		t.Fatalf("%s: obj.self is %T, want the impl", what, obj.self)
	}
	switch i := impl.(type) {
	case *baseObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *arrayObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *funcObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *arrowFuncObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *methodFuncObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *descriptorObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *arrayIterObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	default:
		t.Fatalf("%s: unexpected impl type %T", what, impl)
	}
}

func TestPlainObjectBox(t *testing.T) {
	vm := New()
	obj := vm.NewObject()
	checkWiring(t, "NewObject", obj, obj.self)
	if allocs := testing.AllocsPerRun(100, func() { vm.NewObject() }); allocs != 1 {
		t.Fatalf("NewObject allocated %v times, want 1", allocs)
	}
	sized := vm.NewObjectWithCapacity(4)
	checkWiring(t, "NewObjectWithCapacity", sized, sized.self)
	created := vm.CreateObject(obj)
	checkWiring(t, "CreateObject", created, created.self)
	if created.Prototype() != obj {
		t.Fatal("CreateObject prototype")
	}
	// Object identity is stable and the impl stays reachable through it.
	_ = obj.Set("a", 1)
	if v, ok := obj.GetOwnDataValue("a"); !ok || v.ToInteger() != 1 {
		t.Fatalf("Set/GetOwnDataValue through the box: %v %v", v, ok)
	}
	for _, tc := range []struct {
		src  string
		want float64
	}{
		{"({})", 1},
		{"({a: 1, b: 2, c: 3})", 4},
		{"Object.create(null)", 1},
	} {
		if got := measureProgramAllocs(t, vm, tc.src); got != tc.want {
			t.Errorf("%s: %v allocations, want %v", tc.src, got, tc.want)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -run TestPlainObjectBox -count=1 -v .`
Expected: FAIL with `NewObject allocated 2 times, want 1` (measured at `e41cf0f`; `({})` = 2, `({a: 1, b: 2, c: 3})` = 5 after Task 2, `Object.create(null)` = 2).

- [ ] **Step 4: Box the plain-object constructor**

In `runtime.go` replace:

```go
func (r *Runtime) newBaseObject(proto *Object, class string) (o *baseObject) {
	v := &Object{runtime: r}
	return newBaseObjectObj(v, proto, class)
}
```

with:

```go
// baseObjectBox co-allocates an ordinary object's impl with its Object so
// that creating one costs a single allocation. The impl comes first, so
// &box.baseObject == &box; either pointer keeps the whole box alive. When
// an object later swaps its impl (templated materialisation, guarded to
// base) the box's impl becomes dead weight held by the Object — rare and
// bounded.
type baseObjectBox struct {
	baseObject
	obj Object
}

func (r *Runtime) newBaseObject(proto *Object, class string) *baseObject {
	b := &baseObjectBox{}
	b.obj.runtime = r
	o := &b.baseObject
	o.class = class
	o.val = &b.obj
	o.extensible = true
	o.prototype = proto
	b.obj.self = o
	o.init()
	return o
}
```

(`newBaseObjectObj` stays as it is for callers that already own an `Object`.)

- [ ] **Step 5: Run the tests**

Run: `gofmt -l . ; go vet . && go test -run 'TestCoallocCharacterization|TestPlainObjectBox|TestObjectLiteral|TestNewObjectWithCapacity|TestDescriptor|TestObject|TestJSON' -count=1 . && go test -race -run 'TestCoallocCharacterization|TestPlainObjectBox' . && GOARCH=386 go test -run 'TestCoallocCharacterization|TestPlainObjectBox' .`
Expected: all `ok`; the golden unchanged after the change (record both runs).

- [ ] **Step 6: Run test262**

Run: `go test -run TestTC39 -count=1 . 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add runtime.go object_box_test.go
git commit -m "Allocate ordinary objects and their Object in one box

newBaseObject allocated the public Object and the baseObject separately;
a baseObjectBox holds both, so every plain object (NewObject, literals,
Object.create, JSON) costs one allocation instead of two.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: One allocation per array and array iterator (spec §3, `newArray`, `createArrayIterator`)

**Files:**
- Modify: `builtin_array.go:9-20` (`newArray`)
- Modify: `array.go:53-67` (`createArrayIterator`)
- Test: `object_box_test.go`

**Interfaces:**
- Consumes: `arrayObject`, `arrayIterObject`, `checkWiring` and `measureProgramAllocs` (Tasks 3 and 1).
- Produces: `type arrayObjectBox struct { arrayObject; obj Object }`, `type arrayIterObjectBox struct { arrayIterObject; obj Object }`.

- [ ] **Step 1: Write the failing test**

Append to `object_box_test.go`:

```go
func TestArrayBox(t *testing.T) {
	vm := New()
	arr := vm.NewArray(1, 2)
	checkWiring(t, "NewArray", arr, arr.self)
	if allocs := testing.AllocsPerRun(100, func() { vm.NewArray() }); allocs != 1 {
		t.Fatalf("NewArray() allocated %v times, want 1", allocs)
	}
	// Identity survives the dense -> sparse swap; the box's original impl
	// is simply replaced in obj.self.
	v, err := vm.RunString(`var sw = [1, 2, 3]; sw`)
	if err != nil {
		t.Fatal(err)
	}
	sw := v.(*Object)
	if _, ok := sw.self.(*arrayObject); !ok {
		t.Fatalf("fresh array impl is %T", sw.self)
	}
	if _, err := vm.RunString(`sw[1000000] = 1`); err != nil {
		t.Fatal(err)
	}
	if _, ok := sw.self.(*sparseArrayObject); !ok {
		t.Fatalf("after sparse conversion impl is %T", sw.self)
	}
	// Shrinking does not convert back (that needs >= 1024 items), but the
	// Object identity and contents must be intact behind the swapped impl.
	if _, err := vm.RunString(`sw.length = 3`); err != nil {
		t.Fatal(err)
	}
	if got := vm.Get("sw"); got != Value(sw) {
		t.Fatal("array identity changed across the representation swap")
	}
	if keys := sw.Keys(); len(keys) != 3 || keys[0] != "0" {
		t.Fatalf("keys after the swap: %v", keys)
	}
	if v, ok := sw.GetOwnDataValue("2"); !ok || v.ToInteger() != 3 {
		t.Fatalf("element after the swap: %v %v", v, ok)
	}
	if _, err := vm.RunString(`var arr2 = [1, 2]`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		src  string
		want float64
	}{
		{"[]", 1},
		{"[1, 2, 3]", 2},
		{"for (var x of arr2) {}", 4},
		{"arr2[Symbol.iterator]()", 1},
	} {
		if got := measureProgramAllocs(t, vm, tc.src); got != tc.want {
			t.Errorf("%s: %v allocations, want %v", tc.src, got, tc.want)
		}
	}
	it, _ := vm.RunString(`arr2[Symbol.iterator]()`)
	checkWiring(t, "array iterator", it.(*Object), it.(*Object).self)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestArrayBox -count=1 -v .`
Expected: FAIL with `NewArray() allocated 2 times, want 1` (measured at `e41cf0f`: `[]` = 2, `[1, 2, 3]` = 3, `for…of` = 5, iterator = 2).

- [ ] **Step 3: Box arrays**

In `builtin_array.go` replace `newArray` with:

```go
// arrayObjectBox co-allocates an array's impl with its Object (see
// baseObjectBox). A dense array that converts to sparse gets a new impl in
// obj.self; the boxed one becomes dead weight held by the Object.
type arrayObjectBox struct {
	arrayObject
	obj Object
}

func (r *Runtime) newArray(prototype *Object) (a *arrayObject) {
	b := &arrayObjectBox{}
	b.obj.runtime = r
	a = &b.arrayObject
	a.class = classArray
	a.val = &b.obj
	a.extensible = true
	b.obj.self = a
	a.prototype = prototype
	a.init()
	return
}
```

- [ ] **Step 4: Box array iterators**

In `array.go` replace `createArrayIterator` with:

```go
type arrayIterObjectBox struct {
	arrayIterObject
	obj Object
}

func (r *Runtime) createArrayIterator(iterObj *Object, kind iterationKind) Value {
	b := &arrayIterObjectBox{}
	b.obj.runtime = r
	ai := &b.arrayIterObject
	ai.obj = iterObj
	ai.kind = kind
	ai.class = classObject
	ai.val = &b.obj
	ai.extensible = true
	b.obj.self = ai
	ai.prototype = r.getArrayIteratorPrototype()
	ai.init()
	return &b.obj
}
```

- [ ] **Step 5: Run the tests**

Run: `gofmt -l . ; go vet . && go test -run 'TestCoallocCharacterization|TestArrayBox|TestPlainObjectBox|TestArray|TestIterator|TestSparse|TestJSON' -count=1 . && go test -race -run 'TestCoallocCharacterization|TestArrayBox' . && GOARCH=386 go test -run 'TestCoallocCharacterization|TestArrayBox' .`
Expected: all `ok`; the golden unchanged (record both runs).

- [ ] **Step 6: Run test262**

Run: `go test -run TestTC39 -count=1 . 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add builtin_array.go array.go object_box_test.go
git commit -m "Allocate arrays and array iterators in one box with their Object

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: One allocation per closure and descriptor (spec §3, functions and `newDescriptorObject`)

**Files:**
- Modify: `runtime.go:575-581` (`newFunc`), `:619-627` (`initBaseJsFunction`), `:629-634` (`newMethod`), `:655-666` (`initArrowFunc`, `newArrowFunc`) and the other callers of the two init functions (lines 585, 595, 639, 649, 670)
- Modify: `object_descriptor.go` (`newDescriptorObject`)
- Test: `object_box_test.go`

**Interfaces:**
- Consumes: `funcObject`, `methodFuncObject`, `arrowFuncObject`, `descriptorObject`, `checkWiring`, `measureProgramAllocs`.
- Produces: `initBaseJsFunction(f *baseJsFuncObject, v *Object, strict bool)`, `initArrowFunc(f *arrowFuncObject, v *Object, strict bool)` (both now take the Object), `funcObjectBox`, `methodFuncObjectBox`, `arrowFuncObjectBox`, `descriptorObjectBox`.

- [ ] **Step 1: Write the failing test**

Append to `object_box_test.go`:

```go
func TestFunctionAndDescriptorBox(t *testing.T) {
	vm := New()
	if _, err := vm.RunString(`var o = {a: 1}; var fn = function(){}; var ar = () => 1; var mo = { m() {} }`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fn", "ar"} {
		obj := vm.Get(name).(*Object)
		checkWiring(t, name, obj, obj.self)
	}
	m := vm.Get("mo").(*Object).Get("m").(*Object)
	checkWiring(t, "method", m, m.self)
	gopd, _ := AssertFunction(vm.Get("Object").ToObject(vm).Get("getOwnPropertyDescriptor"))
	dv, err := gopd(Undefined(), vm.Get("o"), vm.ToValue("a"))
	if err != nil {
		t.Fatal(err)
	}
	checkWiring(t, "descriptor", dv.(*Object), dv.(*Object).self)
	for _, tc := range []struct {
		src  string
		want float64
	}{
		{"(function(){})", 1},
		{"(() => 1)", 1},
		{"({ m() {} })", 2},
		{"Object.getOwnPropertyDescriptor(o, 'a')", 1},
	} {
		if got := measureProgramAllocs(t, vm, tc.src); got != tc.want {
			t.Errorf("%s: %v allocations, want %v", tc.src, got, tc.want)
		}
	}
	// Async, generator and class constructors are not boxed and must keep
	// working through the changed init signatures.
	v, err := vm.RunString(`
		var af = async function(){}; var gf = function*(){ yield 1 }; class K { m() { return 1 } }
		[typeof af, gf().next().value, new K().m(), typeof K.prototype.m].join('/')
	`)
	if err != nil || v.String() != "function/1/1/function" {
		t.Fatalf("unboxed function kinds: %v %v", v, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestFunctionAndDescriptorBox -count=1 -v .`
Expected: FAIL with `(function(){}): 2 allocations, want 1` (measured at `e41cf0f`: arrow = 2, descriptor = 2; `({ m() {} })` = 3 after Tasks 2–3: box for the literal + method `Object` + method impl → 2 after this task).

- [ ] **Step 3: Give the init functions the Object**

In `runtime.go` change:

```go
func (r *Runtime) initBaseJsFunction(f *baseJsFuncObject, strict bool) {
	v := &Object{runtime: r}

	f.class = classFunction
	f.val = v
	f.extensible = true
	f.strict = strict
	f.prototype = r.getFunctionPrototype()
}
```

to:

```go
// initBaseJsFunction wires f as the impl of v; callers that box the
// function pass the box's Object, the others a fresh &Object{runtime: r}.
func (r *Runtime) initBaseJsFunction(f *baseJsFuncObject, v *Object, strict bool) {
	f.class = classFunction
	f.val = v
	f.extensible = true
	f.strict = strict
	f.prototype = r.getFunctionPrototype()
}
```

and `initArrowFunc` to:

```go
func (r *Runtime) initArrowFunc(f *arrowFuncObject, v *Object, strict bool) {
	r.initBaseJsFunction(&f.baseJsFuncObject, v, strict)
	f.newTarget = r.vm.newTarget
}
```

Every caller that is NOT boxed in Step 4 — `newAsyncFunc` (line 585), `newGeneratorFunc` (595), `newGeneratorMethod` (639), `newAsyncMethod` (649), `newAsyncArrowFunc` (670), and any other `initBaseJsFunction(`/`initArrowFunc(` caller `grep` finds in `runtime.go`/`func.go` — passes a fresh Object: change `r.initBaseJsFunction(&f.baseJsFuncObject, strict)` to `r.initBaseJsFunction(&f.baseJsFuncObject, &Object{runtime: r}, strict)` (and likewise for `initArrowFunc`). Their existing `f.val.self = f` lines stay.

- [ ] **Step 4: Box `newFunc`, `newMethod`, `newArrowFunc`**

Replace the three constructors with:

```go
// funcObjectBox, methodFuncObjectBox and arrowFuncObjectBox co-allocate a
// closure's impl with its Object (see baseObjectBox). Generator objects
// created from a boxed function get their own impl; the function itself
// never swaps.
type funcObjectBox struct {
	funcObject
	obj Object
}

func (r *Runtime) newFunc(name unistring.String, length int, strict bool) (f *funcObject) {
	b := &funcObjectBox{}
	b.obj.runtime = r
	f = &b.funcObject
	r.initBaseJsFunction(&f.baseJsFuncObject, &b.obj, strict)
	b.obj.self = f
	f.init(name, intToValue(int64(length)))
	return
}

type methodFuncObjectBox struct {
	methodFuncObject
	obj Object
}

func (r *Runtime) newMethod(name unistring.String, length int, strict bool) (f *methodFuncObject) {
	b := &methodFuncObjectBox{}
	b.obj.runtime = r
	f = &b.methodFuncObject
	r.initBaseJsFunction(&f.baseJsFuncObject, &b.obj, strict)
	b.obj.self = f
	f.init(name, intToValue(int64(length)))
	return
}

type arrowFuncObjectBox struct {
	arrowFuncObject
	obj Object
}

func (r *Runtime) newArrowFunc(name unistring.String, length int, strict bool) (f *arrowFuncObject) {
	b := &arrowFuncObjectBox{}
	b.obj.runtime = r
	f = &b.arrowFuncObject
	r.initArrowFunc(f, &b.obj, strict)
	b.obj.self = f
	f.init(name, intToValue(int64(length)))
	return
}
```

- [ ] **Step 5: Box descriptor objects**

In `object_descriptor.go` replace `newDescriptorObject` with:

```go
type descriptorObjectBox struct {
	descriptorObject
	obj Object
}

func (r *Runtime) newDescriptorObject() *descriptorObject {
	b := &descriptorObjectBox{}
	b.obj.runtime = r
	d := &b.descriptorObject
	d.class = classObject
	d.val = &b.obj
	d.extensible = true
	d.prototype = r.global.ObjectPrototype
	b.obj.self = d
	return d
}
```

`TestDescriptorObjectAllocs` (batch 4) asserts "2 allocations beyond a miss"; update that expectation to 1 in `descriptor_object_test.go` and its comment ("the box holding the Object and the descriptorObject") — this is the one intended change to an existing test, and the reason is the batch's purpose.

- [ ] **Step 6: Run the tests**

Run: `gofmt -l . ; go vet ./... && go test -run 'TestCoallocCharacterization|TestFunctionAndDescriptorBox|TestArrayBox|TestPlainObjectBox|TestDescriptor|TestFunc|TestArrow|TestClass|TestAsync|TestGenerator|TestAssertCallable|TestCallIntrinsic' -count=1 . && go test -race -run 'TestCoallocCharacterization|TestFunctionAndDescriptorBox' . && GOARCH=386 go test -run 'TestCoallocCharacterization|TestFunctionAndDescriptorBox|TestDescriptor' .`
Expected: all `ok`; the golden unchanged (record both runs).

- [ ] **Step 7: Run test262**

Run: `go test -run TestTC39 -count=1 . 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 8: Commit**

```bash
git add runtime.go object_descriptor.go object_box_test.go descriptor_object_test.go
git commit -m "Allocate closures and descriptor objects in one box with their Object

initBaseJsFunction/initArrowFunc take the Object instead of allocating
it; newFunc, newMethod and newArrowFunc allocate a box holding both, as
does newDescriptorObject. Async, generator and class constructors keep
their separate Object.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: Final verification and hand-off

**Files:**
- Modify: `docs/superpowers/specs/2026-09-17-allocation-shape-batch5-design.md` (status line; §6 allocation table filled with the measured after-counts)
- Modify: `object_box_test.go` (benchmarks appended)

- [ ] **Step 1: Full suite, race, 386, vet, gofmt**

Run:
```bash
cd /home/lk/Work/goja && gofmt -l . && go vet ./... && go test ./... 2>&1 | tail -5 && go test -race -run 'TestStringCoercion|TestObjectLiteral|TestCoalloc|TestPlainObjectBox|TestArrayBox|TestFunctionAndDescriptorBox|TestDescriptor|TestCallIntrinsic' . && GOARCH=386 go test -run 'TestCoalloc|TestPlainObjectBox|TestArrayBox|TestFunctionAndDescriptorBox|TestObjectLiteral' .
```
Expected: no gofmt/vet output; every package `ok` (root includes test262); race `ok`; 386 `ok`.

- [ ] **Step 2: Benchmarks**

Append to `object_box_test.go`:

```go
func benchmarkLiteral(b *testing.B, body string) {
	vm := New()
	if _, err := vm.RunString(`var o = {a: 1}; var str = 'hello world'; var arr2 = [1, 2]`); err != nil {
		b.Fatal(err)
	}
	prg, err := Compile("b", body, false)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.RunProgram(prg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkObjectLiteral3(b *testing.B) { benchmarkLiteral(b, `({a: 1, b: 2, c: 3})`) }
func BenchmarkArrayLiteral3(b *testing.B)  { benchmarkLiteral(b, `[1, 2, 3]`) }
func BenchmarkClosure(b *testing.B)        { benchmarkLiteral(b, `(function(){})`) }
func BenchmarkForOf2(b *testing.B)         { benchmarkLiteral(b, `for (var x of arr2) {}`) }
func BenchmarkIndexOf(b *testing.B)        { benchmarkLiteral(b, `str.indexOf('l')`) }
```

These benchmarks reference only public API, so they compile at the baseline. Run:
```bash
S=/tmp/claude-1000/-home-lk-Work-goja/8641a0f3-0ab1-40dc-ac18-94f4ab520340/scratchpad
BENCH='BenchmarkObjectLiteral3|BenchmarkArrayLiteral3|BenchmarkClosure|BenchmarkForOf2|BenchmarkIndexOf|BenchmarkGetOwnPropertyDescriptor'
git worktree add --detach $S/base5 e41cf0f
awk '/^func benchmarkLiteral/,0' object_box_test.go > $S/zz_bench_base_test.go   # the helper and the five benchmarks
sed -i '1i package goja\n\nimport "testing"\n' $S/zz_bench_base_test.go
cp $S/zz_bench_base_test.go $S/base5/
(cd $S/base5 && go test -run xxx -bench "$BENCH" -benchmem -count=5 . > $S/bench5-before.txt)
go test -run xxx -bench "$BENCH" -benchmem -count=5 . > $S/bench5-after.txt
~/go/bin/benchstat $S/bench5-before.txt $S/bench5-after.txt
git worktree remove --force $S/base5
```
(`BenchmarkGetOwnPropertyDescriptor` already exists in `descriptor_object_test.go` on both revisions.) Record the `benchstat` table in the task report. Expected direction: allocs/op `ObjectLiteral3` 7 → 4, `ArrayLiteral3` 3 → 2, `Closure` 2 → 1, `ForOf2` 5 → 4, `IndexOf` 3 → 1, `GetOwnPropertyDescriptor` 2 → 1; ns/op and B/op down on every line.

- [ ] **Step 3: Update the spec**

In the spec: change `**Status:** approved design, not yet implemented` to `**Status:** implemented on branch perf/allocation-shape-batch5 (2026-09-17); per-change benchmarks in commit messages`, and in §6's allocation table replace the `{a: 1, b: 2, c: 3}` row's "measured at plan time → …" text with the measured `7 → 4` and add the ten-key literal row `12 → 5`. No other spec edits.

```bash
git add docs/superpowers/specs/2026-09-17-allocation-shape-batch5-design.md object_box_test.go
git commit -m "Mark allocation-shape batch 5 spec as implemented

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

- [ ] **Step 4: Hand-off summary**

Report to the user: the branch name and head commit; the benchstat table; the three goldens (`TestStringCoercionCharacterization`, `TestObjectLiteralCharacterization`, `TestCoallocCharacterization`) confirmed passing before and after; no public API or observable behaviour change; the one existing-test expectation change (`TestDescriptorObjectAllocs` 2 → 1). Qualification: GAP harness against fork `master` `3ebdf77`; no app-side patch is needed for this batch. Do not merge or push; the user decides.
