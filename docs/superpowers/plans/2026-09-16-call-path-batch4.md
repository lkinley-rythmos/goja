# Call Path & Descriptors Batch 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the property map behind every `Object.getOwnPropertyDescriptor` result, the nested VM re-entry behind `Function.prototype.call`/`apply` and bound functions, four per-call micro allocations, and the map growth the host bridge pays when it fills objects whose size it already knows.

**Architecture:** A new `descriptorObject` impl type serves the four descriptor keys from fields and materializes into an ordinary map only on the first define/delete/preventExtensions/foreign write (the batch-3 virtual-intrinsics pattern). `Function.prototype.call`, `Function.prototype.apply` and `boundFuncObject` get their own `vmCall` that rewrites the `[this][callee][args…]` stack window in place and dispatches to the target's `vmCall`, so the target runs in the caller's VM loop. `toPropertyKey`, `_defineOwnProperty`, `assertCallable` and the array builtins stop allocating on their hot paths. `Runtime.NewObjectWithCapacity` pre-sizes an object's map and key list.

**Tech Stack:** Go 1.25 (module floor), `testing.AllocsPerRun` for allocation acceptance, `testing` benchmarks with `-benchmem`, `benchstat` (`~/go/bin/benchstat`), tc39 test262 under `testdata/test262`.

**Spec:** `docs/superpowers/specs/2026-09-16-call-path-descriptors-batch4-design.md`

## Global Constraints

- Go floor `go 1.25.0` (from `go.mod`).
- All work on branch `perf/call-path-batch4` (exists; spec committed at `7f360ba`, branched from `master` at `50ffab1`).
- Every commit must pass: `gofmt -l .` (empty), `go vet ./...`, `go test ./...` with test262 checked out (`testdata/test262` present; Task 0 verifies), `go test -race` on the tests the task adds, and — for Tasks 5, 6 and 7 — `GOARCH=386 go test -run '<the task's tests>' .` (CI has a 386 leg).
- Tests and benchmarks go in goja's own `*_test.go` files (package `goja`). Never commit anything under `testdata/test262`.
- The only public API addition is `func (r *Runtime) NewObjectWithCapacity(n int) *Object`. No public API is removed or changed. No new dependencies.
- Commit messages end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` — exactly, regardless of the implementer's model.
- Characterization tests (Tasks 1, 5, 6): the golden expected strings below were captured from the unmodified engine at `7f360ba`. Each such test MUST pass before the task's engine change and after it; the implementer runs it both times and records both runs. A golden mismatch after the change is a defect in the change, never a reason to edit the expected string. The single intended observable change — the `call`/`apply`/bound intrinsic frame disappearing from `Error.stack` — is *not* in any golden; it has its own test (`TestCallIntrinsicStackShape`, Task 6) that is expected to fail before the change and pass after.
- Allocation acceptance uses `testing.AllocsPerRun` with exact expected counts (zero slack) unless a step says otherwise and explains why. Where a step gives a "before" count, it was measured on the unmodified engine at `7f360ba`; the implementer records the RED run's actual count in the report.
- Stack-window arithmetic in Tasks 6 and 7 uses the layout `call.exec` (`vm.go`) establishes: `this` at `vm.sp-n-2`, `callee` at `vm.sp-n-1`, arguments at `vm.sp-n … vm.sp-1`.
- Angle-bracket placeholders in commit-message templates (`<before>`, `<after>`, `<benchstat output>`) are filled with the measured lines from that task's benchmark step, never left as is.
- No change to `rcx-go-crud`; the app-side patch is documented in spec §8 and handed to the user in Task 8.

---

### Task 0: Prerequisites

**Files:** none modified.

- [ ] **Step 1: Confirm branch, clean tree, test262**

Run:
```bash
cd /home/lk/Work/goja && git branch --show-current && git status --porcelain | wc -l && ls testdata/test262/test | head -2
```
Expected: `perf/call-path-batch4`, `0`, a directory listing. If `testdata/test262` is missing run `./.tc39_test262_checkout.sh`.

- [ ] **Step 2: Green base**

Run:
```bash
cd /home/lk/Work/goja && gofmt -l . && go vet ./... && go test ./... 2>&1 | tail -5
```
Expected: no gofmt output, no vet output, every package `ok` (the root package run includes test262 and takes several minutes).

- [ ] **Step 3: Record the baseline commit**

Run: `git rev-parse HEAD` — expected `7f360ba…`. Write it in the task report; Task 8 diffs against it.

---

### Task 1: `toPropertyKey` and `_defineOwnProperty` allocations (spec §5.1, §5.2)

**Files:**
- Modify: `runtime.go:2698-2700` (`toPropertyKey`)
- Modify: `object.go:670-684` (`_defineOwnProperty`, the `existingValue == nil` branch)
- Create: `micro_alloc_test.go`

**Interfaces:**
- Consumes: `toPropertyKey(key Value) Value` (`runtime.go`), `(*baseObject)._defineOwnProperty(name unistring.String, existingValue Value, descr PropertyDescriptor, throw bool) (Value, bool)` (`object.go`), `(*baseObject).defineOwnPropertyStr`, `(*baseObject).deleteStr`.
- Produces: unchanged signatures. `micro_alloc_test.go` is the home for Tasks 2 and 3's tests too.

- [ ] **Step 1: Write the characterization test and run it on the unmodified engine**

Create `micro_alloc_test.go`:

```go
package goja

import (
	"testing"
)

// microGoldenScript exercises property-key coercion, defineProperty on new
// and existing keys, and array builtins with indices above 255 and above
// 65536 (the bounds of Go's convT64 cache and goja's int cache).
const microGoldenScript = `
var out = [];
var o = {1: 'one', x: 'ex'}; var keyObj = {toString: function(){ return 'x' }}; var sym = Symbol('q'); o[sym] = 'symv';
out.push(JSON.stringify(Object.getOwnPropertyDescriptor(o, 'x')), JSON.stringify(Object.getOwnPropertyDescriptor(o, 1)), JSON.stringify(Object.getOwnPropertyDescriptor(o, '1')), JSON.stringify(Object.getOwnPropertyDescriptor(o, keyObj)), JSON.stringify(Object.getOwnPropertyDescriptor(o, sym)), o.hasOwnProperty(keyObj), o.hasOwnProperty(sym), o.propertyIsEnumerable(1), Reflect.has(o, keyObj), Reflect.get(o, 1), Reflect.deleteProperty(o, keyObj), Object.keys(o).join(','));
var t = {};
Object.defineProperty(t, 'a', {value: 1, writable: true, enumerable: true, configurable: true});
Object.defineProperty(t, 'b', {value: 2});
Object.defineProperty(t, 'c', {value: 3, writable: true});
Object.defineProperty(t, 'a', {value: 11});
Object.defineProperty(t, 'a', {enumerable: false});
Object.defineProperty(t, 'd', {get: function(){ return 'd' }});
Object.defineProperty(t, 'e', {});
Object.defineProperty(t, 'f', {value: undefined, writable: true, enumerable: true, configurable: true});
out.push(JSON.stringify(Object.getOwnPropertyDescriptors(t)), Object.keys(t).join(','), t.f === undefined && t.hasOwnProperty('f'));
try { Object.defineProperty(t, 'b', {value: 22}) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { Object.defineProperty(Object.preventExtensions({}), 'n', {value: 1, writable: true, enumerable: true, configurable: true}) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
out.push(Reflect.defineProperty(Object.preventExtensions({}), 'n', {value: 1, writable: true, enumerable: true, configurable: true}));
var big = new Array(70000); for (var i = 0; i < 70000; i += 7000) big[i] = i;
var idxs = []; big.forEach(function(v, i) { idxs.push(i) }); out.push(idxs.join(','));
out.push(big.findIndex(function(v, i) { return i > 65536 }), big.map(function(v, i) { return i }).filter(function(v) { return v !== undefined }).join(','), big.reduce(function(acc, v, i) { return acc + i }, 0), big.some(function(v, i) { return i === 63000 }), big.every(function(v, i) { return i <= 70000 }), big.findLast(function(v, i) { return true }), big.findLastIndex(function(v, i) { return i < 300 }), big.find(function(v, i) { return i > 255 }));
var dense = []; for (var j = 0; j < 300; j++) dense.push(j);
out.push(dense.filter(function(v, i) { return i >= 255 && i < 258 }).join(','), dense.map(function(v, i) { return i * 2 })[299], dense.indexOf(299), dense.lastIndexOf(256), dense.findIndex(function(v, i) { return i === 256 }));
out.push(Array.from({length: 300}, function(v, i) { return i })[299], [1, 2, 3].flatMap(function(v, i) { return [i] }).join(','), dense.entries().next().value.join(','), Array.prototype.copyWithin.call({length: 3, 0: 'a', 1: 'b', 2: 'c'}, 0, 1)[0]);
var f = function(a, b) { return a - b }; var arr = [3, 1, 2];
out.push(arr.sort(f).join(','), arr.sort(f).join(','), [5, 4].sort(f).join(','));
out.join('|');
`

// microGolden was captured from the unmodified engine at 7f360ba.
const microGolden = `{"value":"ex","writable":true,"enumerable":true,"configurable":true}|{"value":"one","writable":true,"enumerable":true,"configurable":true}|{"value":"one","writable":true,"enumerable":true,"configurable":true}|{"value":"ex","writable":true,"enumerable":true,"configurable":true}|{"value":"symv","writable":true,"enumerable":true,"configurable":true}|true|true|true|true|one|true|1|{"a":{"value":11,"writable":true,"enumerable":false,"configurable":true},"b":{"value":2,"writable":false,"enumerable":false,"configurable":false},"c":{"value":3,"writable":true,"enumerable":false,"configurable":false},"d":{"enumerable":false,"configurable":false},"e":{"writable":false,"enumerable":false,"configurable":false},"f":{"writable":true,"enumerable":true,"configurable":true}}|f|true|TypeError:Cannot redefine property: b|TypeError:Cannot define property n, object is not extensible|false|0,7000,14000,21000,28000,35000,42000,49000,56000,63000|65537|0,7000,14000,21000,28000,35000,42000,49000,56000,63000|315000|true|true||299||255,256,257|598|299|256|256|299|0,1,2|0,0|b|1,2,3|1,2,3|4,5`

func TestMicroCharacterization(t *testing.T) {
	testScript(microGoldenScript, asciiString(microGolden), t)
}
```

Run: `go test -run TestMicroCharacterization -count=1 -v .`
Expected: `--- PASS` on the unmodified engine. If it fails here, the golden or script was mis-transcribed — compare against the plan text; do not proceed until it passes unmodified.

- [ ] **Step 2: Write the failing allocation tests**

Append to `micro_alloc_test.go`:

```go
func TestToPropertyKeyNoAlloc(t *testing.T) {
	vm := New()
	for _, key := range []Value{asciiString("abc"), vm.ToValue("ünicode"), NewSymbol("s")} {
		var got Value
		allocs := testing.AllocsPerRun(100, func() { got = toPropertyKey(key) })
		// unicodeString is a slice type, so compare with SameAs, not ==.
		if !got.SameAs(key) {
			t.Fatalf("toPropertyKey(%v) returned a different value %v", key, got)
		}
		if allocs != 0 {
			t.Fatalf("toPropertyKey(%v) allocated %v times per call, want 0", key, allocs)
		}
	}
	// Non-key values are still coerced.
	if got := toPropertyKey(valueInt(42)); got.String() != "42" {
		t.Fatalf("toPropertyKey(42) = %v", got)
	}
}

func TestDefineNewAllTrueNoAlloc(t *testing.T) {
	vm := New()
	o := vm.NewObject().self.(*baseObject)
	descr := PropertyDescriptor{Value: valueTrue, Writable: FLAG_TRUE, Enumerable: FLAG_TRUE, Configurable: FLAG_TRUE}
	// Warm up so the map and the names slice already have capacity; the
	// measured loop then contains only the define itself and its undo.
	o.defineOwnPropertyStr("x", descr, true)
	o.deleteStr("x", true)
	allocs := testing.AllocsPerRun(100, func() {
		o.defineOwnPropertyStr("x", descr, true)
		o.deleteStr("x", true)
	})
	if allocs != 0 {
		t.Fatalf("defining a new all-true data property allocated %v times per call, want 0", allocs)
	}
	o.defineOwnPropertyStr("x", descr, true)
	if v := o.values["x"]; v != valueTrue {
		t.Fatalf("all-true data property must be stored as the bare value, got %T", v)
	}
	// A partial descriptor on a new key still gets a valueProperty.
	o.defineOwnPropertyStr("y", PropertyDescriptor{Value: valueTrue}, true)
	if p, ok := o.values["y"].(*valueProperty); !ok || p.writable || p.enumerable || p.configurable {
		t.Fatalf("partial descriptor on a new key stored as %#v", o.values["y"])
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -run 'TestToPropertyKeyNoAlloc|TestDefineNewAllTrueNoAlloc' -count=1 -v .`
Expected: both FAIL — `toPropertyKey(abc) allocated 1 times per call` (the `asciiString` re-box; the symbol case passes already) and `defining a new all-true data property allocated 1 times per call` (the discarded `&valueProperty{}`; measured 1 at `7f360ba`).

- [ ] **Step 4: Implement `toPropertyKey`**

In `runtime.go` replace:

```go
func toPropertyKey(key Value) Value {
	return key.ToString()
}
```

with:

```go
// toPropertyKey coerces key to a property key (a String or a *Symbol).
// A value that already is one is returned as is: calling ToString on it
// would re-box the same string into a fresh interface value on every call.
func toPropertyKey(key Value) Value {
	switch key.(type) {
	case String, *Symbol:
		return key
	}
	return key.ToString()
}
```

- [ ] **Step 5: Implement the `_defineOwnProperty` new-property branch**

In `object.go`, inside `_defineOwnProperty`, replace:

```go
	if existingValue == nil {
		if !o.extensible {
			o.val.runtime.typeErrorResult(throw, "Cannot define property %s, object is not extensible", name)
			return nil, false
		}
		existing = &valueProperty{}
	} else {
```

with:

```go
	if existingValue == nil {
		if !o.extensible {
			o.val.runtime.typeErrorResult(throw, "Cannot define property %s, object is not extensible", name)
			return nil, false
		}
		if descr.Writable == FLAG_TRUE && descr.Enumerable == FLAG_TRUE && descr.Configurable == FLAG_TRUE && descr.Value != nil {
			// A new all-true data property is stored as the bare value (the
			// same shortcut below applies to existing properties); allocate
			// no valueProperty just to discard it.
			return descr.Value, true
		}
		existing = &valueProperty{}
	} else {
```

Leave the later `if descr.Writable == FLAG_TRUE && … { return descr.Value, true }` in place — it still serves the existing-property branch.

- [ ] **Step 6: Run the task's tests, the golden, and the object/runtime suites**

Run: `gofmt -l . ; go vet . && go test -run 'TestMicroCharacterization|TestToPropertyKeyNoAlloc|TestDefineNewAllTrueNoAlloc|TestObject|TestDefine|TestRuntime|TestReflect' -count=1 .` then `go test -race -run 'TestMicroCharacterization|TestToPropertyKeyNoAlloc|TestDefineNewAllTrueNoAlloc' .`
Expected: all `ok`; the golden passes unchanged after the change (record both runs in the report).

- [ ] **Step 7: Commit**

```bash
git add runtime.go object.go micro_alloc_test.go
git commit -m "Stop re-boxing property keys and allocating discarded valueProperty structs

toPropertyKey returned key.ToString() even when key already was a String,
re-boxing the same string on every call (36 MB in the GAP profile).
_defineOwnProperty allocated a valueProperty for every new property before
discovering that an all-true data descriptor is stored as the bare value.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: Cache the `assertCallable` method value (spec §5.3)

**Files:**
- Modify: `func.go:71-80` (`baseJsFuncObject` struct), `func.go:294-296`, `func.go:482-484`, `func.go:500-502`, `func.go:844-846`, `func.go:856-858` (the five `assertCallable` methods)
- Test: `micro_alloc_test.go`

**Interfaces:**
- Consumes: `(*baseJsFuncObject).Call`, `(*arrowFuncObject).Call`, `(*asyncFuncObject).Call`, `(*asyncArrowFuncObject).Call`, `(*classFuncObject).Call` — all `func(FunctionCall) Value`.
- Produces: `baseJsFuncObject.callFn func(FunctionCall) Value` (private field, lazily filled).

- [ ] **Step 1: Write the failing test**

Append to `micro_alloc_test.go`:

```go
func TestAssertCallableCached(t *testing.T) {
	vm := New()
	for _, src := range []string{
		"(function(){ return 1 })",
		"(() => 1)",
		"(async function(){})",
		"(async () => 1)",
		"(class {})",
		"({ m() { return 1 } }).m",
	} {
		v, err := vm.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		fn := v.(*Object)
		first, ok := fn.self.assertCallable()
		if !ok || first == nil {
			t.Fatalf("%s: not callable", src)
		}
		allocs := testing.AllocsPerRun(100, func() { fn.self.assertCallable() })
		if allocs != 0 {
			t.Fatalf("%s: assertCallable allocated %v times per call, want 0", src, allocs)
		}
	}
	// The cached func is the live function: calling it twice works and
	// AssertFunction sees the same behaviour.
	v, _ := vm.RunString("(function(a) { return a * 2 })")
	callable, _ := AssertFunction(v)
	for i := 0; i < 2; i++ {
		res, err := callable(Undefined(), valueInt(21))
		if err != nil || res.ToInteger() != 42 {
			t.Fatalf("call %d: %v, %v", i, res, err)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestAssertCallableCached -count=1 -v .`
Expected: FAIL at the first source with `assertCallable allocated 1 times per call` (the escaping `f.Call` method value; `go build -gcflags=-m . 2>&1 | grep 'f.Call escapes'` shows the five sites).

- [ ] **Step 3: Add the field and rewrite the five methods**

In `func.go`, change the `baseJsFuncObject` struct to:

```go
type baseJsFuncObject struct {
	baseFuncObject

	stash   *stash
	privEnv *privateEnv

	prg    *Program
	src    string
	strict bool

	// callFn caches the method value assertCallable returns. A method
	// value escapes to the heap, so without the cache every toCallable or
	// AssertFunction of a JS function allocated a closure per lookup.
	callFn func(FunctionCall) Value
}
```

Replace each of the five `assertCallable` bodies (`classFuncObject`, `baseJsFuncObject`, `arrowFuncObject`, `asyncFuncObject`, `asyncArrowFuncObject`) — each currently `return f.Call, true` — with the same three lines; `f.Call` resolves to that type's own `Call`, and an object has exactly one concrete type, so the shared field never mixes methods:

```go
func (f *classFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	if f.callFn == nil {
		f.callFn = f.Call
	}
	return f.callFn, true
}
```

(repeat verbatim for `*baseJsFuncObject`, `*arrowFuncObject`, `*asyncFuncObject`, `*asyncArrowFuncObject`).

- [ ] **Step 4: Run the tests**

Run: `gofmt -l . ; go vet . && go test -run 'TestAssertCallableCached|TestMicroCharacterization|TestFunc|TestClass|TestAsync|TestArrow' -count=1 . && go test -race -run TestAssertCallableCached .`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
git add func.go micro_alloc_test.go
git commit -m "Cache the assertCallable method value on JS function objects

Returning f.Call as a method value allocates a closure on every
toCallable/AssertFunction lookup; keep it in the function object instead.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: `intToValue` for indices handed to JS (spec §5.4)

**Files:**
- Modify: `builtin_array.go` (the sites listed in Step 3)
- Test: `micro_alloc_test.go`

**Interfaces:**
- Consumes: `intToValue(i int64) Value` (`vm.go`, batch-1 cache), `valueInt`.
- Produces: none.

Background: `valueInt(k)` is a plain int64 type; passing it to `getIdx`/`setOwnIdx`/`hasPropertyIdx`/`deleteIdx` (typed `valueInt` parameters) costs nothing. Boxing happens only where the `valueInt` is stored into a `Value` slot — callback arguments, `createDataPropertyOrThrow`'s key, `return idx`, `[]Value{…}` literals. Only those sites change.

- [ ] **Step 1: Write the failing test**

Append to `micro_alloc_test.go`:

```go
// TestArrayBuiltinIndexBoxing checks that array builtins do not allocate
// per element when handing the index to a callback: allocations must not
// grow with the array length. Both a native callback (no VM re-entry) and
// a JS callback are used; 300 vs 3000 elements straddles the 256-entry
// convT64 cache so the per-element boxing shows up as ~2700 extra allocs.
func TestArrayBuiltinIndexBoxing(t *testing.T) {
	vm := New()
	mk := func(n int) *Object {
		vals := make([]interface{}, n)
		for i := range vals {
			vals[i] = i
		}
		return vm.NewArray(vals...)
	}
	small, large := mk(300), mk(3000)
	nativeFalse := vm.ToValue(func(call FunctionCall) Value { return valueFalse })
	nativeTrue := vm.ToValue(func(call FunctionCall) Value { return valueTrue })
	nativeAcc := vm.ToValue(func(call FunctionCall) Value { return call.Argument(0) })
	jsFalseV, _ := vm.RunString("(function(v, i) { return false })")
	jsTrueV, _ := vm.RunString("(function(v, i) { return true })")
	jsAccV, _ := vm.RunString("(function(acc, v, i) { return acc })")
	jsFalse, jsTrue, jsAcc := jsFalseV.(*Object), jsTrueV.(*Object), jsAccV.(*Object)
	// every stops at the first false and some at the first true, so each
	// gets the callback that makes it visit every element.
	proto := vm.Get("Array").ToObject(vm).Get("prototype").ToObject(vm)
	for _, tc := range []struct {
		method string
		cb     Value
	}{
		{"forEach", nativeFalse}, {"filter", nativeFalse}, {"some", nativeFalse}, {"every", nativeTrue},
		{"find", nativeFalse}, {"findIndex", nativeFalse}, {"findLast", nativeFalse}, {"findLastIndex", nativeFalse},
		{"reduce", nativeAcc}, {"reduceRight", nativeAcc},
		{"forEach", jsFalse}, {"filter", jsFalse}, {"some", jsFalse}, {"every", jsTrue},
		{"find", jsFalse}, {"findIndex", jsFalse}, {"findLast", jsFalse}, {"findLastIndex", jsFalse},
		{"reduce", jsAcc}, {"reduceRight", jsAcc},
	} {
		fn, ok := AssertFunction(proto.Get(tc.method))
		if !ok {
			t.Fatalf("%s: not a function", tc.method)
		}
		run := func(arr *Object) float64 {
			return testing.AllocsPerRun(10, func() {
				if _, err := fn(arr, tc.cb, valueInt(0)); err != nil {
					panic(err)
				}
			})
		}
		if a, b := run(small), run(large); a != b {
			t.Errorf("%s (%T callback): allocations grow with length: %v for 300 elements, %v for 3000", tc.method, tc.cb.(*Object).self, a, b)
		}
	}
	// map's result array grows its backing slice as it fills (a handful of
	// allocations that scale with log(n)), so it is checked separately: the
	// per-element boxing would add at least 2444 allocations between the
	// two sizes; slice growth adds fewer than 16.
	mapFn, _ := AssertFunction(proto.Get("map"))
	runMap := func(arr *Object, cb Value) float64 {
		return testing.AllocsPerRun(10, func() {
			if _, err := mapFn(arr, cb); err != nil {
				panic(err)
			}
		})
	}
	for _, cb := range []Value{nativeFalse, jsFalse} {
		if a, b := runMap(small, cb), runMap(large, cb); b-a >= 16 {
			t.Errorf("map (%T callback): %v allocations for 300 elements, %v for 3000", cb.(*Object).self, a, b)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestArrayBuiltinIndexBoxing -count=1 -v .`
Expected: FAIL for every method: the 3000-element run reports roughly 2744 more allocations than the 300-element run (one 8-byte box per index ≥ 256), and `map` fails its bound.

- [ ] **Step 3: Replace the boxing sites**

In `builtin_array.go`, at each line below (numbers as of `7f360ba`; the surrounding code identifies the site), change only the expression that stores the index into a `Value` slot. The loop variable is `int64` at every site; if one is not, wrap it in `int64(…)`.

| Line | Current | Change to |
|------|---------|-----------|
| 369 | `createDataPropertyOrThrow(a, valueInt(n), p)` | `createDataPropertyOrThrow(a, intToValue(n), p)` |
| 490 | `createDataPropertyOrThrow(a, valueInt(k), nilSafe(o.self.getIdx(from, nil)))` | `createDataPropertyOrThrow(a, intToValue(k), nilSafe(o.self.getIdx(from, nil)))` |
| 623 | `return idx` (in `indexOf`, where `idx := valueInt(n)`) | `return intToValue(n)` |
| 710 | `return idx` (in `lastIndexOf`, where `idx := valueInt(k)`) | `return intToValue(k)` |
| 731, 752, 773, 796, 808, 832, 848 | `fc.Arguments[1] = idx` | `fc.Arguments[1] = intToValue(k)` |
| 895, 939 | `fc.Arguments[2] = idx` | `fc.Arguments[2] = intToValue(k)` |
| 1124, 1144, 1164, 1184 | `fc.Arguments[0], fc.Arguments[1] = kValue, idx` | `fc.Arguments[0], fc.Arguments[1] = kValue, intToValue(k)` |
| 1146, 1186 | `return idx` | `return intToValue(k)` |
| 1291, 1315 | `createDataPropertyOrThrow(a, pk, fromValue)` | `createDataPropertyOrThrow(a, intToValue(k), fromValue)` |
| 1399, 1414 | `createDataPropertyOrThrow(a, pi, iValue)` / `createDataPropertyOrThrow(a, pi, fromValue)` | `createDataPropertyOrThrow(a, intToValue(i), iValue)` / `createDataPropertyOrThrow(a, intToValue(i), fromValue)` |
| 1405 | `createDataPropertyOrThrow(a, valueInt(i), nilSafe(item))` | `createDataPropertyOrThrow(a, intToValue(i), nilSafe(item))` |
| 1546 | `Arguments: []Value{item, idx}` | `Arguments: []Value{item, intToValue(k)}` |

Keep every `idx := valueInt(k)` / `pk := valueInt(k)` / `pi := valueInt(i)` declaration that is still used by a `getIdx`/`hasPropertyIdx`/`setOwnIdx`/`deleteIdx` call; delete a declaration only if `go vet` reports it unused. Then confirm no boxing site remains:

```bash
grep -n "Arguments\[[0-9]\] = idx\|= kValue, idx\|return idx$\|createDataPropertyOrThrow([a-z]*, \(valueInt\|pk\|pi\)\|Value{.*\bidx\b" builtin_array.go
```
Expected: no output.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l . ; go vet . && go test -run 'TestArrayBuiltinIndexBoxing|TestMicroCharacterization|TestArray' -count=1 . && go test -race -run TestArrayBuiltinIndexBoxing .`
Expected: all `ok`; `TestMicroCharacterization` (indices above 255 and 65536) unchanged.

- [ ] **Step 5: Benchmark and record**

Append to `micro_alloc_test.go`:

```go
func BenchmarkArrayFindJSCallback(b *testing.B) {
	vm := New()
	if _, err := vm.RunString(`var arr = []; for (var i = 0; i < 10000; i++) arr.push(i); var pred = function(x) { return x === -1 }`); err != nil {
		b.Fatal(err)
	}
	prg, _ := Compile("b", "arr.find(pred)", false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.RunProgram(prg); err != nil {
			b.Fatal(err)
		}
	}
}
```

Run: `go test -run xxx -bench BenchmarkArrayFindJSCallback -benchmem -count=3 .`
Expected: `allocs/op` ≤ 4 (it was ≈ 9750 at `7f360ba`: one box per index ≥ 256). Put the before/after `allocs/op` in the commit message.

- [ ] **Step 6: Commit**

```bash
git add builtin_array.go micro_alloc_test.go
git commit -m "Box array indices through intToValue in the array builtins

Indices handed to callbacks, returned from indexOf/find*, or used as
createDataPropertyOrThrow keys were boxed with valueInt(k) directly, which
allocates 8 bytes for every index >= 256. Route them through the int cache.

BenchmarkArrayFindJSCallback allocs/op: <before> -> <after>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: `Runtime.NewObjectWithCapacity` (spec §6)

**Files:**
- Modify: `runtime.go:530-532` (after `NewObject`)
- Create: `object_capacity_test.go`

**Interfaces:**
- Consumes: `(*Runtime).newBaseObject(proto *Object, class string) *baseObject`, `baseObject.values`, `baseObject.propNames`.
- Produces: `func (r *Runtime) NewObjectWithCapacity(n int) *Object` (public).

- [ ] **Step 1: Write the failing tests**

Create `object_capacity_test.go`:

```go
package goja

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/dop251/goja/unistring"
)

func TestNewObjectWithCapacity(t *testing.T) {
	vm := New()
	for _, n := range []int{-1, 0} {
		o := vm.NewObjectWithCapacity(n).self.(*baseObject)
		if o.values != nil || o.propNames != nil {
			t.Fatalf("n=%d: storage must stay lazy as in NewObject", n)
		}
	}
	obj := vm.NewObjectWithCapacity(3)
	o := obj.self.(*baseObject)
	if o.values == nil || len(o.propNames) != 0 || cap(o.propNames) != 3 {
		t.Fatalf("n=3: values=%v propNames len=%d cap=%d", o.values != nil, len(o.propNames), cap(o.propNames))
	}
	if obj.ClassName() != classObject || obj.Prototype() != vm.global.ObjectPrototype || !obj.self.isExtensible() {
		t.Fatal("NewObjectWithCapacity must produce an ordinary extensible Object")
	}
	for i, k := range []string{"c", "a", "b"} {
		if err := obj.Set(k, i); err != nil {
			t.Fatal(err)
		}
	}
	if keys := obj.Keys(); !reflect.DeepEqual(keys, []string{"c", "a", "b"}) {
		t.Fatalf("keys %v", keys)
	}
	// Overshooting the hint degrades to ordinary growth.
	for i := 0; i < 20; i++ {
		if err := obj.Set(fmt.Sprintf("k%d", i), i); err != nil {
			t.Fatal(err)
		}
	}
	if keys := obj.Keys(); len(keys) != 23 || keys[0] != "c" || keys[22] != "k19" {
		t.Fatalf("keys after growth: %v", keys)
	}
	if m := obj.Export().(map[string]interface{}); len(m) != 23 || m["k7"] != int64(7) {
		t.Fatalf("export: %v", m)
	}
}

// TestNewObjectWithCapacityNoGrowth: defining n all-true data properties
// into an object pre-sized for n allocates nothing beyond the object
// itself, while the same defines into NewObject() grow the map and the
// key list.
func TestNewObjectWithCapacityNoGrowth(t *testing.T) {
	vm := New()
	keys := []unistring.String{"a", "b", "c", "d", "e", "f", "g", "h"}
	descr := PropertyDescriptor{Value: valueTrue, Writable: FLAG_TRUE, Enumerable: FLAG_TRUE, Configurable: FLAG_TRUE}
	fill := func(o *Object) {
		for _, k := range keys {
			o.self.defineOwnPropertyStr(k, descr, true)
		}
	}
	empty := testing.AllocsPerRun(50, func() { vm.NewObjectWithCapacity(len(keys)) })
	filled := testing.AllocsPerRun(50, func() { fill(vm.NewObjectWithCapacity(len(keys))) })
	if filled != empty {
		t.Fatalf("defining %d properties into a pre-sized object allocated %v extra times", len(keys), filled-empty)
	}
	plain := testing.AllocsPerRun(50, func() { fill(vm.NewObject()) })
	if plain <= filled {
		t.Fatalf("pre-sizing saved nothing: NewObject+fill %v, NewObjectWithCapacity+fill %v", plain, filled)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestNewObjectWithCapacity' -count=1 -v .`
Expected: compile error `vm.NewObjectWithCapacity undefined`.

- [ ] **Step 3: Implement**

In `runtime.go`, directly after `NewObject`:

```go
// NewObjectWithCapacity creates an ordinary object (as NewObject) whose
// property storage is pre-sized for n string-keyed properties, so that
// defining up to n properties causes no map or key-list growth.
// n <= 0 behaves exactly like NewObject.
func (r *Runtime) NewObjectWithCapacity(n int) *Object {
	o := r.newBaseObject(r.global.ObjectPrototype, classObject)
	if n > 0 {
		o.values = make(map[unistring.String]Value, n)
		o.propNames = make([]unistring.String, 0, n)
	}
	return o.val
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l . ; go vet . && go test -run 'TestNewObjectWithCapacity|TestObject' -count=1 . && go test -race -run TestNewObjectWithCapacity .`
Expected: all `ok`. If `TestNewObjectWithCapacityNoGrowth` reports `filled != empty`, Task 1 was not applied (each define would allocate a `valueProperty`) — check `git log`.

- [ ] **Step 5: Commit**

```bash
git add runtime.go object_capacity_test.go
git commit -m "Add Runtime.NewObjectWithCapacity

Hosts that define a known number of properties onto a fresh object can
pre-size its map and key list and avoid the growth allocations.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: Virtual descriptor objects (spec §3)

**Files:**
- Create: `object_descriptor.go`
- Modify: `builtin_object.go:31-74` (`valuePropToDescriptorObject`)
- Modify: `value.go:901-906` (`AppendKeys` type switch)
- Create: `descriptor_object_test.go`

**Interfaces:**
- Consumes: `baseObject` and its methods (`getStrWithOwnProp`, `_setForeignStr`, `setOwnStr`, `defineOwnPropertyStr`, `deleteStr`, `preventExtensions`, `stringKeys`, `iterateStringKeys`, `export`, `_putProp`, `_put`, `appendEnumerableStringKeys`), `valueProperty` fields (`value`, `getterFunc`, `setterFunc`, `accessor`, `writable`, `enumerable`, `configurable`), `propIterItem`, `iterNextFunc`, `stringValueFromRaw`, `(*Runtime).toBoolean(bool) Value`, `valueTrue`/`valueFalse`, `_undefined`, `nilSafe`, `classObject`, `r.global.ObjectPrototype`.
- Produces: `type descriptorObject struct` with `func (r *Runtime) newDescriptorObject() *descriptorObject`; `valuePropToDescriptorObject` unchanged signature (`func (r *Runtime) valuePropToDescriptorObject(desc Value) Value`).

- [ ] **Step 1: Write the characterization test and run it on the unmodified engine**

Create `descriptor_object_test.go`:

```go
package goja

import (
	"reflect"
	"testing"
)

// descriptorGoldenScript observes descriptor objects every way the engine
// exposes them (keys, JSON, in/hasOwnProperty, entries, for-in, toString,
// prototype, extensibility) across data/accessor/partial/absent/index/
// symbol/Proxy sources, then mutates them in every way that must
// materialize (assign, add, delete, defineProperty, freeze, seal,
// preventExtensions, setPrototypeOf, symbol keys, assign/spread, for-in
// with mid-iteration mutation, self-reference, descriptor-of-descriptor).
const descriptorGoldenScript = `
var out = [];
function show(d) {
  if (d === undefined) return 'undef';
  var ks = []; for (var k in d) ks.push(k);
  return Object.keys(d).join(',') + '/' + JSON.stringify(d) + '/' + Object.getOwnPropertyNames(d).join(',') + '/' + Object.prototype.toString.call(d) + '/' + (Object.getPrototypeOf(d) === Object.prototype) + '/' + Object.isExtensible(d) + '/' + ('value' in d) + ',' + ('get' in d) + ',' + d.hasOwnProperty('enumerable') + ',' + d.hasOwnProperty('toString') + '/' + Object.entries(d).map(function(e){ return e[0] + '=' + (typeof e[1] === 'function' ? 'fn' : String(e[1])) }).join(';') + '/' + ks.join(',');
}
var gopd = Object.getOwnPropertyDescriptor;
var o = {a: 1};
Object.defineProperty(o, 'b', {value: 2, writable: false, enumerable: false, configurable: false});
Object.defineProperty(o, 'c', {get: function g(){ return 3 }, set: function s(v){}, enumerable: true, configurable: true});
Object.defineProperty(o, 'd', {get: function g2(){ return 4 }});
Object.defineProperty(o, 'e', {set: function s2(v){}});
var sym = Symbol('s'); o[sym] = 5;
var arr = [1, 2, 3];
out.push(show(gopd(o, 'a')), show(gopd(o, 'b')), show(gopd(o, 'c')), show(gopd(o, 'd')), show(gopd(o, 'e')), show(gopd(o, sym)), show(gopd(o, 'zz')), show(gopd(arr, '1')), show(gopd(arr, 'length')), show(gopd(function f(x){}, 'name')), show(Reflect.getOwnPropertyDescriptor(o, 'a')));
var all = Object.getOwnPropertyDescriptors(o);
out.push(Object.keys(all).join(',') + '/' + JSON.stringify(all));
var p = new Proxy({x: 1}, {getOwnPropertyDescriptor: function(t, k) { out.push('trap:' + String(k)); return Reflect.getOwnPropertyDescriptor(t, k) }});
out.push(show(gopd(p, 'x')));
var d = gopd(o, 'a');
d.value = 9; out.push(show(d));
d.writable = false; out.push(show(d));
d.extra = 'x'; out.push(show(d));
delete d.value; out.push(show(d));
d.value = 10; out.push(show(d));
var d2 = gopd(o, 'a');
delete d2.enumerable; out.push(show(d2));
Object.defineProperty(d2, 'writable', {enumerable: false}); out.push(show(d2) + '/' + JSON.stringify(gopd(d2, 'writable')));
var d3 = gopd(o, 'a');
Object.defineProperty(d3, 'value', {get: function(){ return 'g' }}); out.push(show(d3) + '/' + JSON.stringify(gopd(d3, 'value')));
var d4 = gopd(o, 'c');
d4.get = null; out.push(show(d4));
d4[0] = 'zero'; out.push(show(d4));
var d5 = gopd(o, 'a');
Object.freeze(d5); out.push(show(d5) + '/' + Object.isFrozen(d5) + '/' + JSON.stringify(gopd(d5, 'value')));
try { (function(){ 'use strict'; d5.value = 1 })(); out.push('nothrow') } catch (e) { out.push(e.constructor.name) }
var d6 = gopd(o, 'a');
Object.seal(d6); out.push(show(d6) + '/' + Object.isSealed(d6) + '/' + (delete d6.value));
var d7 = gopd(o, 'a');
Object.preventExtensions(d7); d7.zz = 1; out.push(show(d7));
var d8 = gopd(o, 'a');
Object.setPrototypeOf(d8, {inherited: true}); out.push(show(d8) + '/' + d8.inherited + '/' + ('inherited' in d8) + '/' + d8.hasOwnProperty('inherited'));
var d9 = gopd(o, 'a');
d9[Symbol.iterator] = 1; out.push(show(d9) + '/' + Object.getOwnPropertySymbols(d9).length);
var d10 = gopd(o, 'a');
out.push(JSON.stringify(Object.assign({}, d10)) + '/' + JSON.stringify(Object.assign(d10, {value: 'assigned'})));
var d11 = gopd(o, 'a');
var spread = {...d11}; out.push(JSON.stringify(spread));
var d12 = gopd(o, 'a');
out.push(Object.getOwnPropertyNames(d12).join(',') + '/' + Reflect.ownKeys(d12).join(','));
var d13 = gopd(o, 'a'); var seen = [];
for (var k in d13) { seen.push(k); if (k === 'value') { delete d13.enumerable; d13.late = 1 } }
out.push(seen.join(','));
var t = {}; Object.defineProperty(t, 'q', gopd(o, 'b')); out.push(JSON.stringify(gopd(t, 'q')));
var t2 = {}; Object.defineProperty(t2, 'q', gopd(o, 'c')); out.push(typeof gopd(t2, 'q').get + '/' + gopd(t2, 'q').enumerable);
var da = gopd(o, 'a'), db = gopd(o, 'a'); out.push((da === db) + '/' + (da === da) + '/' + (da == db));
out.push(show(gopd(gopd(o, 'a'), 'value')) + '/' + show(gopd(gopd(o, 'a'), 'nope')));
var d14 = gopd(o, 'a'); d14.value = d14; out.push(Object.keys(d14).join(',') + '/' + (d14.value === d14));
var d15 = gopd(o, 'a'); out.push(Object.getOwnPropertyDescriptors(d15).value.value + '/' + Object.keys(Object.getOwnPropertyDescriptors(d15)).join(','));
out.join('|');
`

// descriptorGolden was captured from the unmodified engine at 7f360ba.
const descriptorGolden = `value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":2,"writable":false,"enumerable":false,"configurable":false}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=2;writable=false;enumerable=false;configurable=false/value,writable,enumerable,configurable|get,set,enumerable,configurable/{"enumerable":true,"configurable":true}/get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/get=fn;set=fn;enumerable=true;configurable=true/get,set,enumerable,configurable|get,set,enumerable,configurable/{"enumerable":false,"configurable":false}/get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/get=fn;set=undefined;enumerable=false;configurable=false/get,set,enumerable,configurable|get,set,enumerable,configurable/{"enumerable":false,"configurable":false}/get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/get=undefined;set=fn;enumerable=false;configurable=false/get,set,enumerable,configurable|value,writable,enumerable,configurable/{"value":5,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=5;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|undef|value,writable,enumerable,configurable/{"value":2,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=2;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":3,"writable":true,"enumerable":false,"configurable":false}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=3;writable=true;enumerable=false;configurable=false/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":"f","writable":false,"enumerable":false,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=f;writable=false;enumerable=false;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|a,b,c,d,e/{"a":{"value":1,"writable":true,"enumerable":true,"configurable":true},"b":{"value":2,"writable":false,"enumerable":false,"configurable":false},"c":{"enumerable":true,"configurable":true},"d":{"enumerable":false,"configurable":false},"e":{"enumerable":false,"configurable":false}}|trap:x|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":9,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=9;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":9,"writable":false,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=9;writable=false;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable,extra/{"value":9,"writable":false,"enumerable":true,"configurable":true,"extra":"x"}/value,writable,enumerable,configurable,extra/[object Object]/true/true/true,false,true,false/value=9;writable=false;enumerable=true;configurable=true;extra=x/value,writable,enumerable,configurable,extra|writable,enumerable,configurable,extra/{"writable":false,"enumerable":true,"configurable":true,"extra":"x"}/writable,enumerable,configurable,extra/[object Object]/true/true/false,false,true,false/writable=false;enumerable=true;configurable=true;extra=x/writable,enumerable,configurable,extra|writable,enumerable,configurable,extra,value/{"writable":false,"enumerable":true,"configurable":true,"extra":"x","value":10}/writable,enumerable,configurable,extra,value/[object Object]/true/true/true,false,true,false/writable=false;enumerable=true;configurable=true;extra=x;value=10/writable,enumerable,configurable,extra,value|value,writable,configurable/{"value":1,"writable":true,"configurable":true}/value,writable,configurable/[object Object]/true/true/true,false,false,false/value=1;writable=true;configurable=true/value,writable,configurable|value,configurable/{"value":1,"configurable":true}/value,writable,configurable/[object Object]/true/true/true,false,false,false/value=1;configurable=true/value,configurable/{"value":true,"writable":true,"enumerable":false,"configurable":true}|value,writable,enumerable,configurable/{"value":"g","writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=g;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/{"enumerable":true,"configurable":true}|get,set,enumerable,configurable/{"get":null,"enumerable":true,"configurable":true}/get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/get=null;set=fn;enumerable=true;configurable=true/get,set,enumerable,configurable|0,get,set,enumerable,configurable/{"0":"zero","get":null,"enumerable":true,"configurable":true}/0,get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/0=zero;get=null;set=fn;enumerable=true;configurable=true/0,get,set,enumerable,configurable|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/false/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/true/{"value":1,"writable":false,"enumerable":true,"configurable":false}|TypeError|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/false/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/true/false|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/false/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/false/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable,inherited/true/true/false|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/1|{"value":1,"writable":true,"enumerable":true,"configurable":true}/{"value":"assigned","writable":true,"enumerable":true,"configurable":true}|{"value":1,"writable":true,"enumerable":true,"configurable":true}|value,writable,enumerable,configurable/value,writable,enumerable,configurable|value,writable,configurable|{"value":2,"writable":false,"enumerable":false,"configurable":false}|function/true|false/true/false|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/undef|value,writable,enumerable,configurable/true|1/value,writable,enumerable,configurable`

func TestDescriptorCharacterization(t *testing.T) {
	testScript(descriptorGoldenScript, asciiString(descriptorGolden), t)
}
```

Run: `go test -run TestDescriptorCharacterization -count=1 -v .`
Expected: `--- PASS` on the unmodified engine. If it fails here, the golden or script was mis-transcribed — compare against the plan text; do not proceed until it passes unmodified.

- [ ] **Step 2: Write the failing Go-side tests**

Append to `descriptor_object_test.go`:

```go
func gopdCallable(t *testing.T, vm *Runtime) Callable {
	t.Helper()
	fn, ok := AssertFunction(vm.Get("Object").ToObject(vm).Get("getOwnPropertyDescriptor"))
	if !ok {
		t.Fatal("Object.getOwnPropertyDescriptor is not callable")
	}
	return fn
}

// TestDescriptorObjectAllocs: a descriptor costs exactly two allocations
// (the Object and the descriptorObject) beyond what a miss (undefined)
// costs, which is the Callable wrapper's own overhead.
func TestDescriptorObjectAllocs(t *testing.T) {
	vm := New()
	gopd := gopdCallable(t, vm)
	target := vm.NewObject()
	if err := target.Set("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := target.DefineAccessorProperty("acc", vm.ToValue(func(FunctionCall) Value { return valueTrue }), nil, FLAG_TRUE, FLAG_TRUE); err != nil {
		t.Fatal(err)
	}
	hit, acc, miss := vm.ToValue("a"), vm.ToValue("acc"), vm.ToValue("nope")
	run := func(key Value) float64 {
		return testing.AllocsPerRun(100, func() {
			if _, err := gopd(Undefined(), target, key); err != nil {
				panic(err)
			}
		})
	}
	base := run(miss)
	for _, key := range []Value{hit, acc} {
		if got := run(key) - base; got != 2 {
			t.Errorf("descriptor for %v: %v allocations beyond a miss, want 2", key, got)
		}
	}
}

func TestDescriptorObjectGoAPI(t *testing.T) {
	vm := New()
	gopd := gopdCallable(t, vm)
	target := vm.NewObject()
	_ = target.Set("a", 1)
	dv, err := gopd(Undefined(), target, vm.ToValue("a"))
	if err != nil {
		t.Fatal(err)
	}
	d := dv.(*Object)
	impl, ok := d.self.(*descriptorObject)
	if !ok {
		t.Fatalf("descriptor is a %T, want *descriptorObject", d.self)
	}
	want := []string{"value", "writable", "enumerable", "configurable"}
	check := func(stage string) {
		t.Helper()
		if keys := d.Keys(); !reflect.DeepEqual(keys, want) {
			t.Fatalf("%s: Keys() = %v", stage, keys)
		}
		if keys := d.AppendKeys(nil); !reflect.DeepEqual(keys, want) {
			t.Fatalf("%s: AppendKeys() = %v", stage, keys)
		}
		if names := d.GetOwnPropertyNames(); !reflect.DeepEqual(names, want) {
			t.Fatalf("%s: GetOwnPropertyNames() = %v", stage, names)
		}
		if v, ok := d.GetOwnDataValue("value"); !ok || v.ToInteger() != 1 {
			t.Fatalf("%s: GetOwnDataValue(value) = %v, %v", stage, v, ok)
		}
		if v, ok := d.GetOwnDataValue("get"); ok || v != nil {
			t.Fatalf("%s: GetOwnDataValue(get) = %v, %v", stage, v, ok)
		}
		m, ok := d.Export().(map[string]interface{})
		if !ok || len(m) != 4 || m["value"] != int64(1) || m["writable"] != true {
			t.Fatalf("%s: Export() = %#v", stage, d.Export())
		}
		if d.ClassName() != classObject || d.Prototype() != vm.global.ObjectPrototype {
			t.Fatalf("%s: class %q proto ok=%v", stage, d.ClassName(), d.Prototype() == vm.global.ObjectPrototype)
		}
	}
	check("virtual")
	// Export materializes (it walks the map); everything still agrees.
	if !impl.materialized {
		t.Fatal("Export must materialize the descriptor object")
	}
	check("materialized")
	if len(impl.values) != 4 || len(impl.propNames) != 4 || cap(impl.propNames) != 5 {
		t.Fatalf("materialized storage: values=%d names=%d cap=%d", len(impl.values), len(impl.propNames), cap(impl.propNames))
	}

	// A fresh descriptor stays virtual through reads and plain assignment
	// to its own keys, and materializes on a new key.
	dv, _ = gopd(Undefined(), target, vm.ToValue("a"))
	d = dv.(*Object)
	impl = d.self.(*descriptorObject)
	_ = d.Get("value")
	_ = d.Set("value", 7)
	_ = d.Set("writable", false)
	if impl.materialized {
		t.Fatal("reads and assignments to the four keys must not materialize")
	}
	if v, _ := d.GetOwnDataValue("value"); v.ToInteger() != 7 {
		t.Fatalf("value after assignment: %v", v)
	}
	if v := d.Get("writable"); v != valueFalse {
		t.Fatalf("writable after assignment: %v", v)
	}
	_ = d.Set("writable", "yes") // non-boolean flag value: materializes
	if !impl.materialized || d.Get("writable").String() != "yes" {
		t.Fatalf("non-boolean flag assignment: materialized=%v writable=%v", impl.materialized, d.Get("writable"))
	}
	dv, _ = gopd(Undefined(), target, vm.ToValue("a"))
	d = dv.(*Object)
	impl = d.self.(*descriptorObject)
	_ = d.Set("extra", 1)
	if !impl.materialized {
		t.Fatal("a new key must materialize")
	}
	if keys := d.Keys(); !reflect.DeepEqual(keys, append(want, "extra")) {
		t.Fatalf("keys after new key: %v", keys)
	}

	// Descriptor objects of accessor properties.
	_ = target.DefineAccessorProperty("acc", vm.ToValue(func(FunctionCall) Value { return valueTrue }), nil, FLAG_TRUE, FLAG_TRUE)
	dv, _ = gopd(Undefined(), target, vm.ToValue("acc"))
	d = dv.(*Object)
	if keys := d.Keys(); !reflect.DeepEqual(keys, []string{"get", "set", "enumerable", "configurable"}) {
		t.Fatalf("accessor descriptor keys: %v", keys)
	}
	if d.Get("set") != _undefined || d.Get("get").ExportType() == nil {
		t.Fatalf("accessor descriptor get/set: %v / %v", d.Get("get"), d.Get("set"))
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -run 'TestDescriptorObjectAllocs|TestDescriptorObjectGoAPI' -count=1 -v .`
Expected: compile error `undefined: descriptorObject` (and, once it compiles, `TestDescriptorObjectAllocs` would report 7 allocations beyond a miss — measured at `7f360ba`: `Object`, `baseObject`, map, bucket, and three `propNames` growths).

- [ ] **Step 4: Create `object_descriptor.go`**

```go
package goja

import "github.com/dop251/goja/unistring"

// descriptorObject is the ordinary object returned by
// Object.getOwnPropertyDescriptor, Object.getOwnPropertyDescriptors and
// Reflect.getOwnPropertyDescriptor. Its four properties live in fields —
// no property map, no key list — until something other than a read or a
// plain assignment to one of them happens (a new key, a define, a delete,
// preventExtensions, an export). Then ensureMaterialized moves them into
// the ordinary map in the same order and the object is a baseObject from
// there on. Nothing observable differs between the two states.
type descriptorObject struct {
	baseObject
	value, get, set                    Value // get/set: *Object or _undefined
	accessor                           bool
	writable, enumerable, configurable bool
	materialized                       bool
}

var (
	descriptorDataKeys     = [...]unistring.String{"value", "writable", "enumerable", "configurable"}
	descriptorAccessorKeys = [...]unistring.String{"get", "set", "enumerable", "configurable"}
)

func (r *Runtime) newDescriptorObject() *descriptorObject {
	v := &Object{runtime: r}
	d := &descriptorObject{}
	d.class = classObject
	d.val = v
	d.extensible = true
	d.prototype = r.global.ObjectPrototype
	v.self = d
	return d
}

// keys returns the virtual key set in enumeration order.
func (d *descriptorObject) keys() []unistring.String {
	if d.accessor {
		return descriptorAccessorKeys[:]
	}
	return descriptorDataKeys[:]
}

// field returns the value of a virtual property, or nil if name is not one
// of the four keys. Every virtual property is a writable, enumerable,
// configurable data property, so the bare value is the stored form.
func (d *descriptorObject) field(name unistring.String) Value {
	r := d.val.runtime
	if d.accessor {
		switch name {
		case "get":
			return d.get
		case "set":
			return d.set
		}
	} else {
		switch name {
		case "value":
			return d.value
		case "writable":
			return r.toBoolean(d.writable)
		}
	}
	switch name {
	case "enumerable":
		return r.toBoolean(d.enumerable)
	case "configurable":
		return r.toBoolean(d.configurable)
	}
	return nil
}

// setField stores val into a virtual property in place and reports whether
// it did. Flags only hold booleans, so any other value for a flag (or any
// key that is not virtual) is left to the caller, which materializes.
func (d *descriptorObject) setField(name unistring.String, val Value) bool {
	if d.accessor {
		switch name {
		case "get":
			d.get = val
			return true
		case "set":
			d.set = val
			return true
		}
	} else {
		switch name {
		case "value":
			d.value = val
			return true
		case "writable":
			if b, ok := val.(valueBool); ok {
				d.writable = bool(b)
				return true
			}
			return false
		}
	}
	if b, ok := val.(valueBool); ok {
		switch name {
		case "enumerable":
			d.enumerable = bool(b)
			return true
		case "configurable":
			d.configurable = bool(b)
			return true
		}
	}
	return false
}

// ensureMaterialized moves the four properties into the ordinary map, in
// enumeration order, sized for them plus the key that is usually about to
// be added. Afterwards every method delegates to baseObject.
func (d *descriptorObject) ensureMaterialized() {
	if d.materialized {
		return
	}
	keys := d.keys()
	d.values = make(map[unistring.String]Value, len(keys)+1)
	d.propNames = make([]unistring.String, 0, len(keys)+1)
	for _, k := range keys {
		d.values[k] = d.field(k)
		d.propNames = append(d.propNames, k)
	}
	d.materialized = true
}

func (d *descriptorObject) getStr(name unistring.String, receiver Value) Value {
	if !d.materialized {
		if v := d.field(name); v != nil {
			return v
		}
	}
	return d.baseObject.getStr(name, receiver)
}

func (d *descriptorObject) getOwnPropStr(name unistring.String) Value {
	if !d.materialized {
		return d.field(name) // nil when absent: there are no other own string keys yet
	}
	return d.baseObject.getOwnPropStr(name)
}

func (d *descriptorObject) hasOwnPropertyStr(name unistring.String) bool {
	if !d.materialized {
		return d.field(name) != nil
	}
	return d.baseObject.hasOwnPropertyStr(name)
}

func (d *descriptorObject) setOwnStr(name unistring.String, val Value, throw bool) bool {
	if !d.materialized {
		if d.setField(name, val) {
			return true
		}
		d.ensureMaterialized()
	}
	return d.baseObject.setOwnStr(name, val, throw)
}

func (d *descriptorObject) setForeignStr(name unistring.String, val, receiver Value, throw bool) (bool, bool) {
	return d._setForeignStr(name, d.getOwnPropStr(name), val, receiver, throw)
}

func (d *descriptorObject) defineOwnPropertyStr(name unistring.String, descr PropertyDescriptor, throw bool) bool {
	d.ensureMaterialized()
	return d.baseObject.defineOwnPropertyStr(name, descr, throw)
}

func (d *descriptorObject) deleteStr(name unistring.String, throw bool) bool {
	if !d.materialized {
		if d.field(name) == nil {
			return true // absent: nothing to delete, stay virtual
		}
		d.ensureMaterialized()
	}
	return d.baseObject.deleteStr(name, throw)
}

func (d *descriptorObject) preventExtensions(throw bool) bool {
	d.ensureMaterialized()
	return d.baseObject.preventExtensions(throw)
}

func (d *descriptorObject) _putProp(name unistring.String, value Value, writable, enumerable, configurable bool) Value {
	d.ensureMaterialized()
	return d.baseObject._putProp(name, value, writable, enumerable, configurable)
}

func (d *descriptorObject) stringKeys(all bool, accum []Value) []Value {
	if d.materialized {
		return d.baseObject.stringKeys(all, accum)
	}
	for _, k := range d.keys() {
		accum = append(accum, stringValueFromRaw(k))
	}
	return accum
}

// appendEnumerableStringKeys serves Object.AppendKeys/Keys.
func (d *descriptorObject) appendEnumerableStringKeys(dst []string) []string {
	if d.materialized {
		return d.baseObject.appendEnumerableStringKeys(dst)
	}
	keys := d.keys()
	if dst == nil {
		dst = make([]string, 0, len(keys))
	}
	for _, k := range keys {
		dst = append(dst, k.String())
	}
	return dst
}

// descriptorPropIter walks the four keys. Like objectPropIter it sees the
// key list as it was when the iteration started (keys added meanwhile are
// not visited) and skips keys deleted meanwhile; if the object
// materializes mid-iteration the remaining keys are read from the map.
type descriptorPropIter struct {
	d   *descriptorObject
	idx int
}

func (i *descriptorPropIter) next() (propIterItem, iterNextFunc) {
	d := i.d
	keys := d.keys()
	for i.idx < len(keys) {
		name := keys[i.idx]
		i.idx++
		var v Value
		if d.materialized {
			v = d.values[name]
		} else {
			v = d.field(name)
		}
		if v != nil {
			return propIterItem{name: stringValueFromRaw(name), value: v}, i.next
		}
	}
	return propIterItem{}, nil
}

func (d *descriptorObject) iterateStringKeys() iterNextFunc {
	if d.materialized {
		return d.baseObject.iterateStringKeys()
	}
	return (&descriptorPropIter{d: d}).next
}

// export walks baseObject's own key list, so materialize first.
func (d *descriptorObject) export(ctx *objectExportCtx) interface{} {
	d.ensureMaterialized()
	return d.baseObject.export(ctx)
}
```

- [ ] **Step 5: Build descriptors with it and route `AppendKeys`**

In `builtin_object.go` replace the whole `valuePropToDescriptorObject` with:

```go
func (r *Runtime) valuePropToDescriptorObject(desc Value) Value {
	if desc == nil {
		return _undefined
	}
	d := r.newDescriptorObject()
	if v, ok := desc.(*valueProperty); ok {
		d.writable, d.enumerable, d.configurable, d.accessor = v.writable, v.enumerable, v.configurable, v.accessor
		if v.accessor {
			d.get, d.set = _undefined, _undefined
			if v.getterFunc != nil {
				d.get = v.getterFunc
			}
			if v.setterFunc != nil {
				d.set = v.setterFunc
			}
		} else {
			d.value = nilSafe(v.value)
		}
	} else {
		d.writable, d.enumerable, d.configurable = true, true, true
		d.value = desc
	}
	return d.val
}
```

In `value.go`, `AppendKeys`, add a case to the type switch:

```go
	switch impl := o.self.(type) {
	case *baseObject:
		return impl.appendEnumerableStringKeys(dst)
	case *arrayObject:
		return impl.appendEnumerableStringKeys(dst)
	case *descriptorObject:
		return impl.appendEnumerableStringKeys(dst)
	}
```

- [ ] **Step 6: Run the task's tests, the golden, and the wider suites**

Run: `gofmt -l . ; go vet . && go test -run 'TestDescriptor|TestMicroCharacterization|TestObject|TestReflect|TestJSON|TestProxy|TestKeys|TestAppendKeys|TestGetOwnDataValue' -count=1 . && go test -race -run 'TestDescriptor' . && GOARCH=386 go test -run 'TestDescriptor' .`
Expected: all `ok` — `TestDescriptorCharacterization` passing after the change is the equivalence proof (record both runs). If it fails, report the differing segment (split on `|`) — do not edit the golden.

- [ ] **Step 7: Run test262**

Run: `go test -run TestTC39 -count=1 . 2>&1 | tail -3`
Expected: `ok`. Descriptor objects are exercised by nearly every `verifyProperty` call in the suite.

- [ ] **Step 8: Benchmark and record**

Append to `descriptor_object_test.go`:

```go
func BenchmarkGetOwnPropertyDescriptor(b *testing.B) {
	vm := New()
	if _, err := vm.RunString(`var o = {a: 1}; var gopd = Object.getOwnPropertyDescriptor`); err != nil {
		b.Fatal(err)
	}
	prg, _ := Compile("b", "gopd(o, 'a').value", false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.RunProgram(prg); err != nil {
			b.Fatal(err)
		}
	}
}
```

Run: `go test -run xxx -bench BenchmarkGetOwnPropertyDescriptor -benchmem -count=3 .`
Expected: 2 allocs/op and roughly a third of the bytes/op of `7f360ba` (measure the baseline with `git stash` if a number is wanted for the commit message; `allocs/op` is the acceptance).

- [ ] **Step 9: Commit**

```bash
git add object_descriptor.go builtin_object.go value.go descriptor_object_test.go
git commit -m "Serve property descriptor objects from fields until they are mutated

Object.getOwnPropertyDescriptor built an ordinary object and wrote four
properties into it through setOwnStr: a prototype walk, a map insert and
a key-list append each, ~600 bytes per descriptor. descriptorObject keeps
the four properties in fields, enumerates them in spec order, and
materializes into the ordinary map only on a define, delete, new key,
preventExtensions or export.

BenchmarkGetOwnPropertyDescriptor: <before> -> <after>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: In-place `Function.prototype.call` and `apply` (spec §4.1, §4.2)

**Files:**
- Modify: `builtin_function.go` (new types after `createListFromArrayLike`; the template lines `t.putStr("apply", …)` and `t.putStr("call", …)` at 241 and 243)
- Modify: `runtime.go:746-763` (factor `newNativeFunc`)
- Create: `call_intrinsics_test.go`

**Interfaces:**
- Consumes: `vm.stack` (`valueStack`, with `expand(idx int)`), `vm.sp`, `vm.r`, `(*Runtime).createListFromArrayLike(a Value) []Value`, `objectImpl.vmCall(vm *vm, n int)`, `nativeFuncObject`, `funcObject`, `methodFuncObject`, `arrowFuncObject`, `boundFuncObject`, `wrappedFuncObject`, `_undefined`, `_null`, `valueProp`.
- Produces: `func vmCallTarget(v Value) *Object` (Task 7 uses it), `type funcProtoCallObject`, `type funcProtoApplyObject`, `func (r *Runtime) initNativeFuncObject(v *Object, f *nativeFuncObject, call func(FunctionCall) Value, name unistring.String, length int)`.

- [ ] **Step 1: Write the characterization test and run it on the unmodified engine**

Create `call_intrinsics_test.go`:

```go
package goja

import (
	"strings"
	"testing"
)

// callApplyGoldenScript pins this-binding, argument counts, arguments
// objects, new.target, apply's array-like handling and error text, bound
// functions (0-2 bound args, bound-of-bound, bound call/apply, new),
// non-callable targets, class/generator/method/arrow/proxy targets, and
// exception propagation. Stack traces are deliberately absent: the
// intrinsic frame disappearing is the one intended change (see
// TestCallIntrinsicStackShape).
const callApplyGoldenScript = `
var out = [];
function sloppy(a, b, c) { return [typeof this, this === undefined ? 'U' : (this === globalThis ? 'G' : (typeof this === 'object' ? 'O' : String(this))), arguments.length, a, b, c, new.target === undefined].join(':') }
function strict(a, b, c) { 'use strict'; return [typeof this, this === undefined ? 'U' : (this === globalThis ? 'G' : (typeof this === 'object' ? 'O' : String(this))), arguments.length, a, b, c, new.target === undefined].join(':') }
var arrow = (a, b) => [typeof this, a, b].join(':');
var native = Object.prototype.hasOwnProperty;
var obj = {k: 1};
var fs = {sloppy: sloppy, strict: strict};
for (var name in fs) {
  var f = fs[name];
  out.push(f.call(), f.call(undefined), f.call(null), f.call(7), f.call('s'), f.call(obj), f.call(obj, 1), f.call(obj, 1, 2), f.call(obj, 1, 2, 3), f.call(obj, 1, 2, 3, 4));
  out.push(f.apply(), f.apply(obj), f.apply(obj, undefined), f.apply(obj, null), f.apply(obj, []), f.apply(obj, [1]), f.apply(obj, [1, 2, 3, 4]), f.apply(obj, [1, , 3]), f.apply(obj, {length: 2, 0: 'x', 1: 'y'}), f.apply(obj, (function(){ return arguments })('p', 'q')), f.apply(obj, new Array(3)), f.apply(obj, [1, 2, 3], 'extra'));
  var sparse = [1, 2, 3]; sparse[10] = 11; out.push(f.apply(obj, sparse));
  try { f.apply(obj, 5) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
  try { f.apply(obj, 'str') } catch (e) { out.push(e.constructor.name + ':' + e.message) }
  var b0 = f.bind(), b1 = f.bind(obj), b2 = f.bind(obj, 1), b3 = f.bind(obj, 1, 2), bb = f.bind(obj, 1).bind({other: 1}, 2);
  out.push(b0(), b0(9), b1(), b1(9), b2(), b2(9), b3(), b3(9), bb(), bb(9), b2.call({x: 1}, 8), b2.apply({x: 1}, [8, 7]), b2.length, b2.name);
  out.push(typeof new b1(1, 2), (new b1(1, 2)) instanceof f);
}
out.push(arrow.call(obj, 1, 2), arrow.apply(obj, [1, 2]), arrow.bind(obj, 1)(2));
out.push(native.call(obj, 'k'), native.call(obj, 'z'), native.apply(obj, ['k']), Function.prototype.call.bind(native)(obj, 'k'), Function.prototype.call.bind(native)(obj, 'z'), Function.prototype.apply.bind(native)(obj, ['k']));
var has = Function.prototype.call.bind(native); out.push(has(obj, 'k'), has.call(null, obj, 'k'), has.apply(null, [obj, 'k']));
function thrower() { throw new RangeError('boom') }
try { thrower.call(obj) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { thrower.apply(obj, []) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { thrower.bind(obj)() } catch (e) { out.push(e.constructor.name + ':' + e.message) }
var bads = [undefined, null, 1, 'x', {}, Symbol.iterator];
for (var i = 0; i < bads.length; i++) {
  try { Function.prototype.call.call(bads[i], obj) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
  try { Function.prototype.apply.call(bads[i], obj, []) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
}
class K { constructor(x) { this.x = x } static s(a) { return 'static:' + a + ':' + (this === K) } }
out.push(K.s.call(K, 1), K.s.call(null, 2));
try { K.call(obj, 1) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
function* gen(a) { yield a; yield this === obj }
var g = gen.call(obj, 'g'); out.push(g.next().value + ':' + g.next().value);
var meth = {m(a) { return [this === obj, a, new.target === undefined].join(':') }}.m;
out.push(meth.call(obj, 'm'), meth.apply(obj, ['m2']), meth.bind(obj)('m3'));
var asy = async function(a) { return this === obj ? 'async:' + a : 'bad' };
asy.call(obj, 1).then(function(v) { out.push(v) });
out.push(String.prototype.toUpperCase.call('abc'), Array.prototype.join.call('abc', '-'), Math.max.apply(null, [1, 5, 3]), Math.max.apply(Math, {length: 2, 0: 4, 1: 9}));
function rec(n) { return n === 0 ? 0 : 1 + rec.call(null, n - 1) }
out.push(rec(500));
function mapped(a) { arguments[0] = 'changed'; return a }
out.push(mapped.call(null, 'orig'), mapped.apply(null, ['orig']), mapped.bind(null)('orig'));
function who() { return this === obj ? 'obj' : (this === undefined ? 'undef' : 'other') }
out.push(who.bind(obj).call({}), who.bind(obj).bind({})(), who.call(), (function(){ 'use strict'; return who.bind()() })());
var callee = null; function cap() { callee = arguments.callee; return arguments.length } out.push(cap.call(obj, 1, 2), callee === cap);
var proxyFn = new Proxy(function(a) { return 'px:' + a + ':' + (this === obj) }, {}); out.push(proxyFn.call(obj, 1), proxyFn.apply(obj, [2]), proxyFn.bind(obj)(3));
out.join('|');
`

// callApplyGolden was captured from the unmodified engine at 7f360ba.
const callApplyGolden = `object:G:0::::true|object:G:0::::true|object:G:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:1:1:::true|object:O:2:1:2::true|object:O:3:1:2:3:true|object:O:4:1:2:3:true|object:G:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:1:1:::true|object:O:4:1:2:3:true|object:O:3:1::3:true|object:O:2:x:y::true|object:O:2:p:q::true|object:O:3::::true|object:O:3:1:2:3:true|object:O:11:1:2:3:true|TypeError:Value is not an object: 5|TypeError:Value is not an object: str|object:G:0::::true|object:G:1:9:::true|object:O:0::::true|object:O:1:9:::true|object:O:1:1:::true|object:O:2:1:9::true|object:O:2:1:2::true|object:O:3:1:2:9:true|object:O:2:1:2::true|object:O:3:1:2:9:true|object:O:2:1:8::true|object:O:3:1:8:7:true|2|bound sloppy|object|true|undefined:U:0::::true|undefined:U:0::::true|object:O:0::::true|number:7:0::::true|string:s:0::::true|object:O:0::::true|object:O:1:1:::true|object:O:2:1:2::true|object:O:3:1:2:3:true|object:O:4:1:2:3:true|undefined:U:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:1:1:::true|object:O:4:1:2:3:true|object:O:3:1::3:true|object:O:2:x:y::true|object:O:2:p:q::true|object:O:3::::true|object:O:3:1:2:3:true|object:O:11:1:2:3:true|TypeError:Value is not an object: 5|TypeError:Value is not an object: str|undefined:U:0::::true|undefined:U:1:9:::true|object:O:0::::true|object:O:1:9:::true|object:O:1:1:::true|object:O:2:1:9::true|object:O:2:1:2::true|object:O:3:1:2:9:true|object:O:2:1:2::true|object:O:3:1:2:9:true|object:O:2:1:8::true|object:O:3:1:8:7:true|2|bound strict|object|true|object:1:2|object:1:2|object:1:2|true|false|true|true|false|true|true|true|true|RangeError:boom|RangeError:boom|RangeError:boom|TypeError:Value is not an object: undefined|TypeError:Value is not an object: undefined|TypeError:Value is not an object: null|TypeError:Value is not an object: null|TypeError:Value is not an object: 1|TypeError:Value is not an object: 1|TypeError:Value is not an object: x|TypeError:Value is not an object: x|TypeError:Value is not callable: [object Object]|TypeError:Value is not callable: [object Object]|TypeError:Value is not an object: Symbol.iterator|TypeError:Value is not an object: Symbol.iterator|static:1:true|static:2:false|TypeError:Class constructor cannot be invoked without 'new'|g:true|true:m:true|true:m2:true|true:m3:true|ABC|a-b-c|5|9|500|changed|changed|changed|obj|obj|other|other|2|true|px:1:true|px:2:true|px:3:true`

func TestCallApplyBindCharacterization(t *testing.T) {
	testScript(callApplyGoldenScript, asciiString(callApplyGolden), t)
}

// TestCallIntrinsicOverflow: unbounded recursion through call/apply/bind
// surfaces as *StackOverflowError exactly as direct recursion does, before
// and after the change (the depth at which it triggers differs; the type
// does not).
func TestCallIntrinsicOverflow(t *testing.T) {
	for _, src := range []string{
		`function d() { return d() } d()`,
		`function inf() { return inf.call(null) } inf()`,
		`function infA() { return infA.apply(null, []) } infA()`,
		`var infB = function() { return infB.bind(null)() }; infB()`,
	} {
		vm := New()
		vm.SetMaxCallStackSize(200)
		_, err := vm.RunString(src)
		if _, ok := err.(*StackOverflowError); !ok {
			t.Errorf("%s: got %T (%v), want *StackOverflowError", src, err, err)
		}
	}
}
```

Run: `go test -run 'TestCallApplyBindCharacterization|TestCallIntrinsicOverflow' -count=1 -v .`
Expected: both `--- PASS` on the unmodified engine. If the golden fails here it was mis-transcribed — compare against the plan text; do not proceed until it passes unmodified.

- [ ] **Step 2: Write the failing stack-shape and allocation tests**

Append to `call_intrinsics_test.go`:

```go
// TestCallIntrinsicStackShape pins the one intended observable change:
// call/apply/bound no longer appear as "(native)" frames because the
// target runs in the caller's VM loop, exactly as a direct call does. The
// scripts are byte-identical to the ones the pre-change traces were taken
// from, so the remaining frames' positions are unchanged. A builtin that
// re-enters the VM (map) keeps its frame — the control case.
func TestCallIntrinsicStackShape(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{
			`function inner() { return new Error('x').stack } function outer() { return inner.call(null, 1) } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat outer (<eval>:1:86(5))", "\tat <eval>:1:103(4)"},
		},
		{
			`function inner() { return new Error('x').stack } function outer() { return inner.apply(null, [1]) } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat outer (<eval>:1:87(7))", "\tat <eval>:1:106(4)"},
		},
		{
			`function inner() { return new Error('x').stack } function outer() { return inner.bind(null)(1) } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat outer (<eval>:1:92(7))", "\tat <eval>:1:103(4)"},
		},
		{
			`function inner() { return new Error('x').stack } var b = Function.prototype.call.bind(inner); function outer() { return b(null, 1) } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat outer (<eval>:1:122(4))", "\tat <eval>:1:139(12)"},
		},
		{
			`function inner() { return new Error('x').stack } function outer() { return [1].map(inner)[0] } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat map (native)", "\tat outer (<eval>:1:83(6))", "\tat <eval>:1:101(4)"},
		},
	} {
		vm := New()
		v, err := vm.RunString(tc.src)
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Split(strings.TrimRight(v.String(), "\n"), "\n")
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// TestCallIntrinsicsNoAlloc: f.call(t, a), f.apply(t, arr) with a dense
// array, and bound(a) allocate nothing beyond the direct call f(a).
func TestCallIntrinsicsNoAlloc(t *testing.T) {
	vm := New()
	if _, err := vm.RunString(`
		var f = function(a, b) { return a };
		var n = Object.prototype.hasOwnProperty;
		var o = {k: 1};
		var args = [1, 2];
		var bf = f.bind(o, 1);
		var bn = Function.prototype.call.bind(n);
	`); err != nil {
		t.Fatal(err)
	}
	measure := func(src string) float64 {
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
	direct := measure("f(1, 2)")
	directNative := measure("o.hasOwnProperty('k')")
	for _, tc := range []struct {
		src  string
		base float64
	}{
		{"f.call(o, 1, 2)", direct},
		{"f.call(o)", direct},
		{"f.apply(o, args)", direct},
		{"f.apply(o)", direct},
		{"bf(2)", direct},
		{"n.call(o, 'k')", directNative},
		{"bn(o, 'k')", directNative},
	} {
		if got := measure(tc.src); got != tc.base {
			t.Errorf("%s: %v allocations per run, direct call %v", tc.src, got, tc.base)
		}
	}
}
```

Note for `TestCallIntrinsicsNoAlloc`: with Task 2 in place the `call`/`apply` rows already pass (the `toCallable` method value was their only per-call allocation), so this task's RED signal is `TestCallIntrinsicStackShape`; the allocation test guards the rewrite against regressions. `bf(2)` and `bn(o, 'k')` (one allocation each: the bound closure's `append`) pass only after Task 7, as do the two bound cases of the stack-shape test.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test -run 'TestCallIntrinsicStackShape|TestCallIntrinsicsNoAlloc' -count=1 -v .`
Expected: `TestCallIntrinsicStackShape` FAILs on the first four cases (traces contain `at call (native)` / `at apply (native)` / `at bound inner (native)` / `at bound call (native)`) and passes the `map` control; `TestCallIntrinsicsNoAlloc` FAILs on exactly `bf(2)` and `bn(o, 'k')` (+1 allocation each) and passes the `call`/`apply` rows. Record the actual output.

- [ ] **Step 4: Factor `newNativeFunc` so wrapper types can reuse it**

In `runtime.go` replace `newNativeFunc` with:

```go
// initNativeFuncObject fills f — a nativeFuncObject, possibly embedded in
// a wrapper type — as the implementation of the function object v.
func (r *Runtime) initNativeFuncObject(v *Object, f *nativeFuncObject, call func(FunctionCall) Value, name unistring.String, length int) {
	f.baseObject = baseObject{
		class:      classFunction,
		val:        v,
		extensible: true,
		prototype:  r.getFunctionPrototype(),
	}
	f.f = call
	f.init(name, intToValue(int64(length)))
}

func (r *Runtime) newNativeFunc(call func(FunctionCall) Value, name unistring.String, length int) *Object {
	v := &Object{runtime: r}
	f := &nativeFuncObject{}
	r.initNativeFuncObject(v, f, call, name, length)
	v.self = f
	return v
}
```

(`baseFuncObject.init` sets `lenProp`/`nameProp`/`intrinsicsVirtual`; the struct literal assignment before it is exactly what the old code did inline.)

- [ ] **Step 5: Add the intrinsic types**

In `builtin_function.go`, after `createListFromArrayLike`:

```go
// vmCallTarget reports whether v is a function whose vmCall may be entered
// directly with a rewritten stack window, returning it if so. The list is
// exactly the impl types whose Call and vmCall are the same operation:
// anything else — generators, async functions, classes, proxies,
// non-callables — returns nil and takes the ordinary native path, keeping
// its current behaviour and error messages.
func vmCallTarget(v Value) *Object {
	obj, ok := v.(*Object)
	if !ok {
		return nil
	}
	switch f := obj.self.(type) {
	case *funcObject, *methodFuncObject, *arrowFuncObject, *boundFuncObject:
		return obj
	case *nativeFuncObject:
		if f.f != nil {
			return obj
		}
	case *wrappedFuncObject:
		if f.f != nil {
			return obj
		}
	}
	return nil
}

// funcProtoCallObject is the Function.prototype.call intrinsic. Invoked by
// the VM with an eligible target it rewrites the stack window
// [f][call][thisArg][a1 … a(n-1)] into [thisArg][f][a1 … a(n-1)] and
// dispatches to f's vmCall, so f.call(t, a) costs what f(a) costs: no
// nested run loop, no try frame, no extra context. Go callers (via
// assertCallable) and ineligible targets take the native path.
type funcProtoCallObject struct {
	nativeFuncObject
}

func (f *funcProtoCallObject) vmCall(vm *vm, n int) {
	target := vmCallTarget(vm.stack[vm.sp-n-2])
	if target == nil {
		f.nativeFuncObject.vmCall(vm, n)
		return
	}
	base := vm.sp - n - 2
	var thisArg Value = _undefined
	if n > 0 {
		thisArg = vm.stack[base+2]
		copy(vm.stack[base+2:vm.sp-1], vm.stack[base+3:vm.sp])
		vm.sp--
		n--
	}
	vm.stack[base] = thisArg
	vm.stack[base+1] = target
	target.self.vmCall(vm, n)
}

// funcProtoApplyObject is the Function.prototype.apply intrinsic; see
// funcProtoCallObject. [f][apply][thisArg][argArray][ignored …] becomes
// [thisArg][f][elements …]. createListFromArrayLike runs before the
// window is rewritten, so a TypeError for a non-object argArray leaves the
// frame intact and carries today's message.
type funcProtoApplyObject struct {
	nativeFuncObject
}

func (f *funcProtoApplyObject) vmCall(vm *vm, n int) {
	target := vmCallTarget(vm.stack[vm.sp-n-2])
	if target == nil {
		f.nativeFuncObject.vmCall(vm, n)
		return
	}
	base := vm.sp - n - 2
	var thisArg, argArray Value = _undefined, _undefined
	if n > 0 {
		thisArg = vm.stack[base+2]
	}
	if n > 1 {
		argArray = vm.stack[base+3]
	}
	var args []Value
	if argArray != _undefined && argArray != _null {
		args = vm.r.createListFromArrayLike(argArray)
	}
	vm.stack[base] = thisArg
	vm.stack[base+1] = target
	vm.sp = base + 2
	vm.stack.expand(vm.sp + len(args))
	for _, a := range args {
		if a == nil {
			a = _undefined
		}
		vm.stack[vm.sp] = a
		vm.sp++
	}
	target.self.vmCall(vm, len(args))
}

func (r *Runtime) newFuncProtoCall() *Object {
	v := &Object{runtime: r}
	f := &funcProtoCallObject{}
	r.initNativeFuncObject(v, &f.nativeFuncObject, r.functionproto_call, "call", 1)
	v.self = f
	return v
}

func (r *Runtime) newFuncProtoApply() *Object {
	v := &Object{runtime: r}
	f := &funcProtoApplyObject{}
	r.initNativeFuncObject(v, &f.nativeFuncObject, r.functionproto_apply, "apply", 2)
	v.self = f
	return v
}
```

Then change the two template lines (currently `return r.methodProp(r.functionproto_apply, "apply", 2)` and `return r.methodProp(r.functionproto_call, "call", 1)`) to:

```go
	t.putStr("apply", func(r *Runtime) Value { return valueProp(r.newFuncProtoApply(), true, false, true) })
```
and
```go
	t.putStr("call", func(r *Runtime) Value { return valueProp(r.newFuncProtoCall(), true, false, true) })
```

(`methodProp` is `valueProp(r.newNativeFunc(f, name, nArgs), true, false, true)`, so the property flags are unchanged.)

- [ ] **Step 6: Run the tests**

Run: `gofmt -l . ; go vet . && go test -run 'TestCallApplyBindCharacterization|TestCallIntrinsicOverflow|TestCallIntrinsicStackShape|TestCallIntrinsicsNoAlloc|TestFunc|TestBind|TestArguments|TestStack' -count=1 -v . 2>&1 | grep -v '^=== RUN' | tail -40 && go test -race -run 'TestCallApplyBindCharacterization|TestCallIntrinsicStackShape' . && GOARCH=386 go test -run 'TestCallApplyBindCharacterization|TestCallIntrinsicStackShape' .`
Expected: the golden and overflow tests PASS; `TestCallIntrinsicStackShape` passes the `call`, `apply` and `map` cases and still fails the two bound cases; `TestCallIntrinsicsNoAlloc` passes every row except `bf(2)` and `bn(o, 'k')` (Task 7). Everything else `ok`. If the golden fails, report the differing segment (split on `|`) — do not edit the golden.

- [ ] **Step 7: Run test262**

Run: `go test -run TestTC39 -count=1 . 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 8: Benchmark and record**

Append to `call_intrinsics_test.go`:

```go
func benchmarkProgram(b *testing.B, setup, body string) {
	vm := New()
	if _, err := vm.RunString(setup); err != nil {
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

const callBenchSetup = `var f = function(s, x) { return (s + x) & 1023 }; var n = Object.prototype.hasOwnProperty; var o = {k: 1};`

func BenchmarkDirectCall(b *testing.B) {
	benchmarkProgram(b, callBenchSetup, `(function(){ var s = 0; for (var i = 0; i < 1000; i++) s = f(s, i); return s })()`)
}
func BenchmarkFuncProtoCall(b *testing.B) {
	benchmarkProgram(b, callBenchSetup, `(function(){ var s = 0; for (var i = 0; i < 1000; i++) s = f.call(null, s, i); return s })()`)
}
func BenchmarkFuncProtoApply(b *testing.B) {
	benchmarkProgram(b, callBenchSetup, `(function(){ var s = 0, a = [0, 0]; for (var i = 0; i < 1000; i++) { a[0] = s; a[1] = i; s = f.apply(null, a) } return s })()`)
}
func BenchmarkNativeCall(b *testing.B) {
	benchmarkProgram(b, callBenchSetup, `(function(){ var s = 0; for (var i = 0; i < 1000; i++) if (n.call(o, 'k')) s++; return s })()`)
}
```

Run: `go test -run xxx -bench 'BenchmarkDirectCall|BenchmarkFuncProtoCall|BenchmarkFuncProtoApply|BenchmarkNativeCall' -benchmem -count=5 . | tee /tmp/claude-1000/-home-lk-Work-goja/8641a0f3-0ab1-40dc-ac18-94f4ab520340/scratchpad/bench-task6-after.txt`
Expected: `BenchmarkFuncProtoCall` within ~10% of `BenchmarkDirectCall` in ns/op and equal in allocs/op (at `7f360ba` the loop was ≈ 1.7× the direct-call loop's ns/op; the +1 alloc per call it also had is already gone since Task 2). Put the before/after lines in the commit message (baseline: `git stash; go test -run xxx -bench … -count=5 . > …before.txt; git stash pop`, then `~/go/bin/benchstat before.txt after.txt`).

- [ ] **Step 9: Commit**

```bash
git add runtime.go builtin_function.go call_intrinsics_test.go
git commit -m "Run Function.prototype.call/apply targets in the caller's VM loop

The call and apply intrinsics were ordinary native functions: every
f.call(t, a) re-entered the VM through __call with a try frame, a second
context and a nested run loop, roughly three times the per-call cost of f(a). They now rewrite the
[this][callee][args] window in place and dispatch to the target's vmCall.
Ineligible targets keep the native path; the only observable change is
that the intrinsic no longer appears as a (native) frame in Error.stack.

<benchstat output>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 7: In-place bound function invocation (spec §4.3)

**Files:**
- Modify: `func.go:140-143` (`boundFuncObject` struct)
- Modify: `builtin_function.go` (`functionproto_bind`, the `bf := &boundFuncObject{…}` literal; new `vmCall` method next to the Task 6 types)
- Test: `call_intrinsics_test.go`

**Interfaces:**
- Consumes: `vmCallTarget(v Value) *Object` (Task 6), `boundFuncObject{nativeFuncObject, wrapped *Object}`, `(*Runtime).boundCallable` (unchanged; still backs the Go-facing `Call`).
- Produces: `boundFuncObject.boundThis Value`, `boundFuncObject.boundArgs []Value`, `func (f *boundFuncObject) vmCall(vm *vm, n int)`.

- [ ] **Step 1: Confirm the failing rows**

Run: `go test -run 'TestCallIntrinsicStackShape|TestCallIntrinsicsNoAlloc' -count=1 -v .`
Expected: exactly the bound rows fail — `inner.bind(null)(1)` and `Function.prototype.call.bind(inner)` in the stack-shape test, `bf(2)` and `bn(o, 'k')` in the allocation test (+1 each: the bound closure's `append(args, call.Arguments...)`).

- [ ] **Step 2: Add the fields**

In `func.go` change the struct to:

```go
type boundFuncObject struct {
	nativeFuncObject
	wrapped *Object
	// boundThis and boundArgs duplicate what the native closure captured so
	// that vmCall can rewrite the stack window without calling it.
	boundThis Value
	boundArgs []Value
}
```

- [ ] **Step 3: Populate them in `functionproto_bind`**

In `builtin_function.go`, replace:

```go
	bf := &boundFuncObject{
		nativeFuncObject: *ff,
		wrapped:          obj,
	}
```

with:

```go
	bf := &boundFuncObject{
		nativeFuncObject: *ff,
		wrapped:          obj,
		boundThis:        _undefined,
	}
	if len(call.Arguments) > 0 {
		bf.boundThis = call.Arguments[0]
		// call.Arguments aliases the VM stack; copy, as boundCallable does.
		bf.boundArgs = append([]Value(nil), call.Arguments[1:]...)
	}
```

- [ ] **Step 4: Add `vmCall`**

In `builtin_function.go`, after `funcProtoApplyObject.vmCall`:

```go
// vmCall rewrites [ignored this][bound][a0 … a(n-1)] into
// [boundThis][target][boundArgs …][a0 … a(n-1)] and dispatches to the
// wrapped function's vmCall. Construction (new bound()) is unaffected: it
// goes through assertConstructor. An ineligible wrapped function (a class,
// a generator, …) takes the native closure path as before.
func (f *boundFuncObject) vmCall(vm *vm, n int) {
	target := vmCallTarget(f.wrapped)
	if target == nil {
		f.nativeFuncObject.vmCall(vm, n)
		return
	}
	base := vm.sp - n - 2
	if k := len(f.boundArgs); k > 0 {
		vm.stack.expand(vm.sp + k)
		copy(vm.stack[base+2+k:base+2+k+n], vm.stack[base+2:base+2+n])
		copy(vm.stack[base+2:base+2+k], f.boundArgs)
		vm.sp += k
		n += k
	}
	vm.stack[base] = f.boundThis
	vm.stack[base+1] = target
	target.self.vmCall(vm, n)
}
```

(`copy` is a memmove, so the overlapping shift is safe; `expand` is called before any slice of `vm.stack` is taken because it may reallocate.)

- [ ] **Step 5: Run the tests**

Run: `gofmt -l . ; go vet . && go test -run 'TestCallApplyBindCharacterization|TestCallIntrinsicOverflow|TestCallIntrinsicStackShape|TestCallIntrinsicsNoAlloc|TestFunc|TestBind' -count=1 . && go test -race -run 'TestCallApplyBindCharacterization|TestCallIntrinsicsNoAlloc' . && GOARCH=386 go test -run 'TestCallApplyBindCharacterization|TestCallIntrinsicStackShape|TestCallIntrinsicsNoAlloc' .`
Expected: everything `ok` — every row of `TestCallIntrinsicStackShape` and `TestCallIntrinsicsNoAlloc` now passes; the golden is unchanged (record both runs). If the golden fails, report the differing segment — do not edit it.

- [ ] **Step 6: Run test262**

Run: `go test -run TestTC39 -count=1 . 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 7: Benchmark and record**

Append to `call_intrinsics_test.go`:

```go
func BenchmarkBoundCall(b *testing.B) {
	benchmarkProgram(b, callBenchSetup+` var bf = f.bind(null, 0); var has = Function.prototype.call.bind(n);`,
		`(function(){ var s = 0; for (var i = 0; i < 1000; i++) { s = bf(i); if (has(o, 'k')) s++ } return s })()`)
}
```

Run: `go test -run xxx -bench 'BenchmarkBoundCall|BenchmarkDirectCall|BenchmarkNativeCall' -benchmem -count=5 .`
Expected: `BenchmarkBoundCall` allocs/op equal to `BenchmarkDirectCall` + `BenchmarkNativeCall` (i.e. no per-call allocation; before this task it was +2 per iteration, one for each bound call). Put the before/after lines in the commit message.

- [ ] **Step 8: Commit**

```bash
git add func.go builtin_function.go call_intrinsics_test.go
git commit -m "Invoke bound functions from the VM without the native closure

boundFuncObject keeps boundThis/boundArgs and rewrites the stack window
in place, so bound(a) and Function.prototype.call.bind(fn)(t, a) run the
target in the caller's VM loop with no per-call slice allocation. The Go
Call path still uses the closure.

<benchstat output>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 8: Final verification and hand-off

**Files:**
- Modify: `docs/superpowers/specs/2026-09-16-call-path-descriptors-batch4-design.md` (status line and the §7 allocation table row for `NewObjectWithCapacity`)

- [ ] **Step 1: Full suite, race, 386, vet, gofmt**

Run:
```bash
cd /home/lk/Work/goja && gofmt -l . && go vet ./... && go test ./... 2>&1 | tail -5 && go test -race -run 'TestMicro|TestToPropertyKey|TestDefineNew|TestAssertCallable|TestArrayBuiltinIndexBoxing|TestNewObjectWithCapacity|TestDescriptor|TestCall' . && GOARCH=386 go test -run 'TestDescriptor|TestCall|TestArrayBuiltinIndexBoxing' .
```
Expected: no gofmt/vet output; every package `ok` (root includes test262); race `ok`; 386 `ok`.

- [ ] **Step 2: Whole-branch benchmark summary**

The benchmark files reference symbols that do not exist at the baseline, so the baseline gets a self-contained copy of just the benchmarks. Create `$S/zz_bench_base_test.go` (package `goja`) containing, copied verbatim from the branch: `benchmarkProgram`, `callBenchSetup`, `BenchmarkDirectCall`, `BenchmarkFuncProtoCall`, `BenchmarkFuncProtoApply`, `BenchmarkNativeCall`, `BenchmarkBoundCall` (from `call_intrinsics_test.go`), `BenchmarkGetOwnPropertyDescriptor` (from `descriptor_object_test.go`) and `BenchmarkArrayFindJSCallback` (from `micro_alloc_test.go`). Then:

```bash
S=/tmp/claude-1000/-home-lk-Work-goja/8641a0f3-0ab1-40dc-ac18-94f4ab520340/scratchpad
BENCH='BenchmarkGetOwnPropertyDescriptor|BenchmarkDirectCall|BenchmarkFuncProtoCall|BenchmarkFuncProtoApply|BenchmarkNativeCall|BenchmarkBoundCall|BenchmarkArrayFindJSCallback'
git worktree add $S/base 7f360ba && cp $S/zz_bench_base_test.go $S/base/
(cd $S/base && go test -run xxx -bench "$BENCH" -benchmem -count=5 . > $S/bench-before.txt)
go test -run xxx -bench "$BENCH" -benchmem -count=5 . > $S/bench-after.txt
~/go/bin/benchstat $S/bench-before.txt $S/bench-after.txt
git worktree remove --force $S/base
```

Record the `benchstat` table in the task report. Expected direction: `BenchmarkGetOwnPropertyDescriptor` allocs/op 9 → 2; `BenchmarkFuncProtoCall`/`BenchmarkFuncProtoApply` ns/op down to about `BenchmarkDirectCall`'s, allocs/op −1; `BenchmarkBoundCall` allocs/op −2; `BenchmarkArrayFindJSCallback` allocs/op ≈ 9750 → ≤ 4.

- [ ] **Step 3: Update the spec**

In the spec: change `**Status:** approved design, not yet implemented` to `**Status:** implemented on branch perf/call-path-batch4 (2026-09-16); per-change benchmarks in commit messages`, and in §7's allocation table change the `NewObjectWithCapacity(n)` row's expected count from `3 (`Object`, map, names)` to `no allocations beyond NewObjectWithCapacity(n) itself` (the measured invariant Task 4 asserts; the absolute count depends on the Go map implementation).

```bash
git add docs/superpowers/specs/2026-09-16-call-path-descriptors-batch4-design.md
git commit -m "Mark call-path batch 4 spec as implemented

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

- [ ] **Step 4: Hand-off summary**

Report to the user: the branch name and head commit; the benchstat table; the list of goldens (`TestMicroCharacterization`, `TestDescriptorCharacterization`, `TestCallApplyBindCharacterization`) confirmed passing before and after; the one observable change (`TestCallIntrinsicStackShape`); the new public API (`NewObjectWithCapacity`); and the spec §8 app-side patch for `rcx-go-crud` (four `rt.NewObject()` → `rt.NewObjectWithCapacity(len(fields))` sites: `goja_bridge.go:120`, `goja_bridge.go:161`, `goja_owned.go:521`, `goja_context.go:235`) together with the reminder that the batch-2 §8 `GetOwnDataValue` patch should be in place before the GAP qualification run. Do not merge or push; the user decides.
