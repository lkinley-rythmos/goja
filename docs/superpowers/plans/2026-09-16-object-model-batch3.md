# Object Model Batch 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the property maps arrays and functions allocate just to hold `length`/`name`, the per-clone regexp2 rebuild for global patterns, and the allocations in host-side key enumeration — five independent, individually measured engine changes.

**Architecture:** `baseObject.values` becomes lazily allocated (one `ensureValues()` at every write site). Dense arrays keep `length` purely virtual (a field already exists; enumeration/delete/has gain a `length` branch yielding the field's pointer). Functions keep `length`/`name` in fields behind one `intrinsicsVirtual` flag on `baseFuncObject`, with property-method overrides that consult the fields first and materialize both into the map (at the front of `propNames`) only when a define/delete needs the map. `compileRegexpUncached` builds the regexp2 engine eagerly for global/sticky Go-regexp patterns. `Object.Keys` gets a direct `propNames` walk for plain objects and arrays, exposed as `AppendKeys`.

**Tech Stack:** Go 1.25 (module floor), `testing` benchmarks with `-benchmem`, `benchstat` (`~/go/bin/benchstat`), tc39 test262 under `testdata/test262`.

**Spec:** `docs/superpowers/specs/2026-09-16-object-model-batch3-design.md`

## Global Constraints

- Go floor `go 1.25.0` (from `go.mod`).
- All work on branch `perf/object-model-batch3` (exists; spec committed at `43617bb`, branched from `master` at `2a30572`).
- Every commit must pass: `gofmt -l .` (empty), `go vet ./...`, `go test ./...` with test262 checked out (`testdata/test262` present; Task 0 verifies), `go test -race` on the tests the task adds, and — for Tasks 1–3 — `GOARCH=386 go test -run '<the task's tests>' .` (CI has a 386 leg).
- Tests and benchmarks go in goja's own `*_test.go` files (package `goja`). Never commit anything under `testdata/test262`.
- The only public API addition is `func (o *Object) AppendKeys(dst []string) []string`. No new dependencies.
- Commit messages end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` — exactly, regardless of the implementer's model.
- Characterization tests (Tasks 2 and 3): the golden expected strings below were captured from the unmodified engine at `43617bb`. Each such test MUST pass before the task's engine change and after it; the implementer runs it both times and records both runs. A golden mismatch after the change is a defect in the change, never a reason to edit the expected string.
- Benchmark acceptance is on `allocs/op` (deterministic): each task captures its own baseline before implementing; expected figures are measured here, never carried over from a spike. Time deltas are directional.
- No change to `rcx-go-crud`.

---

### Task 0: Prerequisites

**Files:** none modified.

- [ ] **Step 1: Confirm branch, clean tree, test262**

Run:
```bash
cd /home/lk/Work/goja && git branch --show-current && git status --porcelain | wc -l && ls testdata/test262/test | head -2
```
Expected: `perf/object-model-batch3`, `0`, a directory listing. If `testdata/test262` is missing run `./.tc39_test262_checkout.sh`.

- [ ] **Step 2: Green base**

Run (timeout 600000 ms):
```bash
cd /home/lk/Work/goja && mkdir -p /tmp/goja-bench && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6
```
Expected: all `ok`; root package > 10 s (test262 executing).

---

### Task 1: Lazy property map (spec §3)

**Files:**
- Modify: `object.go` — `baseObject.init` (~line 272), `setOwnStr` (two writes), `defineOwnPropertyStr` (one write), `_put` (one write); add `ensureValues`
- Modify: `object_template.go` — lines ~113, ~231, ~255, ~269
- Create: `lazy_props_test.go` (also holds a test-only accessor method on `arrayObject`)

**Interfaces:**
- Produces: `func (o *baseObject) ensureValues()` — every later task that writes into `values` must call it first.

- [ ] **Step 1: Add the benchmark and capture the baseline**

Create `lazy_props_test.go`:

```go
package goja

import "testing"

func runBenchClosure(b *testing.B, setup string) {
	b.Helper()
	r := New()
	v, err := r.RunString(setup)
	if err != nil {
		b.Fatal(err)
	}
	f, ok := AssertFunction(v)
	if !ok {
		b.Fatal("setup did not return a function")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f(nil); err != nil {
			b.Fatal(err)
		}
	}
}

// Array iterator objects never receive an own property; with a lazy map
// their creation should cost only the Object and the iterator struct.
func BenchmarkArrayIterObjectCreate(b *testing.B) {
	runBenchClosure(b, `var a = [1]; (function() { var n = 0; for (var i = 0; i < 1000; i++) { var it = a[Symbol.iterator](); n += it.next().value; } return n; })`)
}
```

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkArrayIterObjectCreate$' -benchmem -count=10 . > /tmp/goja-bench/b3-task1-base.txt 2>&1 && grep -E "^Benchmark" /tmp/goja-bench/b3-task1-base.txt | head -1
```
Expected: a row with allocs/op in the thousands (per iterator: Object + iterator struct + map, plus the `next()` result object). Record it.

- [ ] **Step 2: Write the failing tests**

Append to `lazy_props_test.go`:

```go
func propMap(t *testing.T, r *Runtime, src string) map[unistring.String]Value {
	t.Helper()
	v, err := r.RunString(src)
	if err != nil {
		t.Fatal(err)
	}
	switch impl := v.(*Object).self.(type) {
	case *baseObject:
		return impl.values
	case *arrayObject:
		return impl.values0()
	case *arrayIterObject:
		return impl.values
	case *dateObject:
		return impl.values
	case *regexpObject:
		return impl.values
	default:
		t.Fatalf("unexpected impl %T", impl)
		return nil
	}
}

func TestLazyPropertyMap(t *testing.T) {
	r := New()
	// RegExp objects are excluded: regexpObject.init stores lastIndex eagerly.
	for _, src := range []string{`({})`, `new Date(0)`, `[][Symbol.iterator]()`} {
		if m := propMap(t, r, src); m != nil {
			t.Errorf("%s: expected nil property map before any write, got len %d", src, len(m))
		}
	}
	if m := propMap(t, r, `(function() { var o = {}; o.x = 1; return o; })()`); m == nil {
		t.Fatal("expected the map to exist after a write")
	}
	// Behaviour on never-written objects must be unchanged.
	r.testScript(`
		var d = new Date(0);
		[Object.keys(d).length, d.hasOwnProperty("x"), delete d.x, JSON.stringify(d), Object.getOwnPropertyNames(d).length,
		 (function() { var k = []; for (var p in d) k.push(p); return k.length; })(),
		 Object.isFrozen(Object.freeze({})), Object.isExtensible(Object.preventExtensions({}))].join(",")`,
		asciiString("0,false,true,\"1970-01-01T00:00:00.000Z\",0,0,true,false"), t)
	// Templated objects (the global object) still materialize and honour the white-hole delete marker.
	r.testScript(`
		var before = typeof parseInt;
		delete globalThis.parseInt;
		var after = typeof parseInt;
		globalThis.parseInt = 42;
		[before, after, parseInt, Object.getOwnPropertyDescriptor(globalThis, "Math").writable].join(",")`,
		asciiString("function,undefined,42,true"), t)
}
```

Also append to `lazy_props_test.go` (a test-file method on the package type; `arrayObject.values` is the element slice, so the base map needs its own accessor):

```go
// values0 exposes the base property map to tests.
func (a *arrayObject) values0() map[unistring.String]Value { return a.baseObject.values }
```

Ensure `lazy_props_test.go` imports `"github.com/dop251/goja/unistring"`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd /home/lk/Work/goja && go test -run 'TestLazyPropertyMap' . 2>&1 | tail -6`
Expected: FAIL with `expected nil property map before any write` for all four sources (the map is allocated eagerly today).

- [ ] **Step 4: Implement**

In `object.go`, replace `baseObject.init` with:

```go
// init deliberately leaves values nil: the map is allocated by
// ensureValues on the first write. Objects that never receive an own
// property (iterators, Dates, RegExps, empty arrays, most functions) never
// pay for one.
func (o *baseObject) init() {
}

// ensureValues allocates the property map on first write. Every site that
// assigns into values must call it; a forgotten site panics on the nil map.
func (o *baseObject) ensureValues() {
	if o.values == nil {
		o.values = make(map[unistring.String]Value)
	}
}
```

In `object.go` `setOwnStr`, the new-property branch:
```go
		} else {
			o.ensureValues()
			o.values[name] = val
			names := copyNamesIfNeeded(o.propNames, 1)
			o.propNames = append(names, name)
		}
```
and the plain-slot overwrite at the end of the function:
```go
	} else {
		o.ensureValues()
		o.values[name] = val
	}
```

In `object.go` `defineOwnPropertyStr`:
```go
	if v, ok := o._defineOwnProperty(name, existingVal, descr, throw); ok {
		o.ensureValues()
		o.values[name] = v
```

In `object.go` `_put`:
```go
func (o *baseObject) _put(name unistring.String, v Value) {
	o.ensureValues()
	if _, exists := o.values[name]; !exists {
		names := copyNamesIfNeeded(o.propNames, 1)
		o.propNames = append(names, name)
	}

	o.values[name] = v
}
```

In `object_template.go`, insert `o.ensureValues()` immediately before each of the four assignments `o.values[p] = v` (in `getOwnPropStr`), `o.values[name] = v` (in `defineOwnPropertyStr`), `o.values[name] = nil // white hole` (in `deleteStr`), and `o.values[name] = f(o.val.runtime)` (in `materialiseProps` — place the call once, before the loop).

Then confirm no write site was missed:
```bash
cd /home/lk/Work/goja && grep -n "\.values\[[^]]*\] = " object.go object_template.go | grep -v "arr\.\|a\.values\|ar\.values"
```
Expected: exactly the eight assignment lines edited above (four in object.go including `_put`, four in object_template.go) — each preceded (within a few lines) by an `ensureValues()` call.

- [ ] **Step 5: Run the tests, then race and 386**

Run:
```bash
cd /home/lk/Work/goja && go test -run 'TestLazyPropertyMap' -v . 2>&1 | grep -E '^(--- |ok|FAIL|panic)' && go test -race -run 'TestLazyPropertyMap' . 2>&1 | tail -1 && GOARCH=386 go test -run 'TestLazyPropertyMap' . 2>&1 | tail -1
```
Expected: `--- PASS`; `ok`; `ok`.

- [ ] **Step 6: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkArrayIterObjectCreate$' -benchmem -count=10 . > /tmp/goja-bench/b3-task1.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/b3-task1-base.txt /tmp/goja-bench/b3-task1.txt
```
Acceptance: allocs/op decreases by at least 1000 (one map per iterator object; the `next()` result object still allocates — it is a direct call, not the iterator protocol). If not, STOP and report.

- [ ] **Step 7: Full suite and static checks**

Run (timeout 600000 ms): `cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6`
Expected: clean; all `ok` including test262. A `panic: assignment to entry in nil map` anywhere means a missed write site — find it with the Step 4 grep and add `ensureValues()`; do not revert the lazy init.

- [ ] **Step 8: Commit**

```bash
cd /home/lk/Work/goja && git add object.go object_template.go lazy_props_test.go && git commit -F - <<'MSG'
Allocate the property map on first write

baseObject.init no longer creates the values map; ensureValues does so at
the first assignment. Objects that never receive an own property —
iterators, Dates, RegExps, empty arrays — stop paying for a map they
never use. Reads, deletes and enumeration are already nil-safe.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 2: Dense array `length` without a map entry (spec §4)

**Files:**
- Modify: `array.go` — `init` (~line 74), `hasOwnPropertyStr`, `deleteStr`, `stringKeys`, `arrayPropIter`/`next` (~lines 284–308)
- Modify: `array_sparse.go` — the sparse→dense conversion (~line 332)
- Test: `lazy_props_test.go` (append)

**Interfaces:**
- Consumes: `ensureValues` (Task 1) — not called directly here; `_put`/`_delete` handle it.
- Produces: invariant "a dense `arrayObject` never has `"length"` in `values`/`propNames`", relied on by Task 5's array fast path.

- [ ] **Step 1: Add the benchmark and capture the baseline**

Append to `lazy_props_test.go`:

```go
func BenchmarkArrayLiteralCreate(b *testing.B) {
	runBenchClosure(b, `(function() { var n = 0; for (var i = 0; i < 1000; i++) { var a = []; n += a.length; } return n; })`)
}
```

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkArrayLiteralCreate$' -benchmem -count=10 . > /tmp/goja-bench/b3-task2-base.txt 2>&1 && grep -E "^Benchmark" /tmp/goja-bench/b3-task2-base.txt | head -1
```
Expected: ≈ 4000 allocs/op (Object + arrayObject + map + map group per array). Record it.

- [ ] **Step 2: Write the characterization test and confirm it passes on the unmodified engine**

Append to `lazy_props_test.go`:

```go
const arrayLengthGoldenScript = `
function dump(a) {
	return [
		Object.getOwnPropertyNames(a).join(","),
		Object.keys(a).join(","),
		(function() { var k = []; for (var p in a) k.push(p); return k.join(","); })(),
		JSON.stringify(a),
		JSON.stringify(Object.getOwnPropertyDescriptor(a, "length")),
		Object.keys(Object.assign({}, a)).join(","),
		[...a].length
	].join("/");
}
var out = [];
var a = [1, 2, 3]; a.x = 9;
out.push(dump(a));
out.push(delete a.length);
out.push((function() { "use strict"; try { delete a.length; return "no"; } catch (e) { return e.constructor.name; } })());
Object.defineProperty(a, "length", { value: 2 }); out.push(dump(a));
Object.defineProperty(a, "length", { writable: false }); out.push(dump(a));
out.push((function() { "use strict"; try { a.push(1); return "no"; } catch (e) { return e.constructor.name; } })());
var b = [1]; Object.freeze(b); out.push(Object.isFrozen(b), JSON.stringify(Object.getOwnPropertyDescriptor(b, "length")));
var c = []; c.foo = 1; c[0] = 2; out.push(dump(c));
var s = []; s[100000] = 1; s.y = 2;
out.push(Object.getOwnPropertyNames(s).join(","), s.length);
for (var i = 0; i < 20000; i++) s[i] = i;
out.push(Object.getOwnPropertyNames(s).length, s.length, Object.keys(s).length, JSON.stringify(Object.getOwnPropertyDescriptor(s, "length")));
var e = []; out.push(Object.getOwnPropertyNames(e).join(","), Object.keys(e).length, e.hasOwnProperty("length"), e.hasOwnProperty("x"), delete e.x);
out.join("|");
`

// Captured from the unmodified engine at 43617bb. Must pass before and after Task 2.
const arrayLengthGolden = `0,1,2,length,x/0,1,2,x/0,1,2,x/[1,2,3]/{"value":3,"writable":true,"enumerable":false,"configurable":false}/0,1,2,x/3|false|TypeError|0,1,length,x/0,1,x/0,1,x/[1,2]/{"value":2,"writable":true,"enumerable":false,"configurable":false}/0,1,x/2|0,1,length,x/0,1,x/0,1,x/[1,2]/{"value":2,"writable":false,"enumerable":false,"configurable":false}/0,1,x/2|TypeError|true|{"value":1,"writable":false,"enumerable":false,"configurable":false}|0,length,foo/0,foo/0,foo/[2]/{"value":1,"writable":true,"enumerable":false,"configurable":false}/0,foo/1|100000,length,y|100001|20003|100001|20002|{"value":100001,"writable":true,"enumerable":false,"configurable":false}|length|0|true|false|true`

func TestArrayLengthCharacterization(t *testing.T) {
	testScript(arrayLengthGoldenScript, asciiString(arrayLengthGolden), t)
}
```

Run: `cd /home/lk/Work/goja && go test -run 'TestArrayLengthCharacterization' -v . 2>&1 | tail -3`
Expected: `--- PASS` on the unmodified engine. If it fails here, the golden or script was mis-transcribed — compare against the plan text; do not proceed until it passes unmodified.

- [ ] **Step 3: Write the failing invariant test**

Append to `lazy_props_test.go`:

```go
func TestArrayLengthNotInMap(t *testing.T) {
	r := New()
	for _, src := range []string{`[]`, `[1, 2]`, `(function() { var a = [1]; a.x = 1; return a; })()`, `(function() { var a = []; Object.defineProperty(a, "length", { value: 3 }); return a; })()`} {
		v, err := r.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		a := v.(*Object).self.(*arrayObject)
		if _, ok := a.baseObject.values["length"]; ok {
			t.Errorf("%s: length is in the property map", src)
		}
		for _, n := range a.propNames {
			if n == "length" {
				t.Errorf("%s: length is in propNames", src)
			}
		}
	}
	// A sparse array converted back to dense must not inherit the sparse map entry.
	v, err := r.RunString(`(function() { var s = []; s[100000] = 1; for (var i = 0; i < 20000; i++) s[i] = i; return s; })()`)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := v.(*Object).self.(*arrayObject)
	if !ok {
		t.Fatalf("expected a dense array after conversion, got %T", v.(*Object).self)
	}
	if _, ok := a.baseObject.values["length"]; ok {
		t.Error("converted dense array inherited length from the sparse map")
	}
}
```

Run: `cd /home/lk/Work/goja && go test -run 'TestArrayLengthNotInMap' . 2>&1 | tail -4`
Expected: FAIL with `length is in the property map` (today `init` puts it there).

- [ ] **Step 4: Implement**

In `array.go`:

`init`:
```go
func (a *arrayObject) init() {
	a.baseObject.init()
	a.lengthProp.writable = true
	// length is virtual: getOwnPropStr, setOwnStr, defineOwnPropertyStr,
	// hasOwnPropertyStr, deleteStr and the key enumerators special-case it,
	// so it is never stored in values/propNames.
}
```

`hasOwnPropertyStr` — add the `length` case:
```go
func (a *arrayObject) hasOwnPropertyStr(name unistring.String) bool {
	if idx := strToArrayIdx(name); idx != math.MaxUint32 {
		return idx < uint32(len(a.values)) && a.values[idx] != nil
	}
	if name == "length" {
		return true
	}
	return a.baseObject.hasOwnPropertyStr(name)
}
```

`deleteStr` — add the `length` case:
```go
func (a *arrayObject) deleteStr(name unistring.String, throw bool) bool {
	if idx := strToArrayIdx(name); idx != math.MaxUint32 {
		return a._deleteIdxProp(idx, throw)
	}
	if name == "length" {
		return a.checkDelete(name, &a.lengthProp, throw)
	}
	return a.baseObject.deleteStr(name, throw)
}
```

`stringKeys` — emit `length` after the indices when `all`:
```go
func (a *arrayObject) stringKeys(all bool, accum []Value) []Value {
	for i, prop := range a.values {
		name := strconv.Itoa(i)
		if prop != nil {
			if !all {
				if prop, ok := prop.(*valueProperty); ok && !prop.enumerable {
					continue
				}
			}
			accum = append(accum, asciiString(name))
		}
	}
	if all {
		accum = append(accum, asciiString("length"))
	}
	return a.baseObject.stringKeys(all, accum)
}
```

`arrayPropIter` — yield `length` once (with the field pointer, so Object.freeze mutates the real storage) between the indices and the map:
```go
type arrayPropIter struct {
	a          *arrayObject
	limit      int
	idx        int
	lengthDone bool
}

func (i *arrayPropIter) next() (propIterItem, iterNextFunc) {
	for i.idx < len(i.a.values) && i.idx < i.limit {
		name := asciiString(strconv.Itoa(i.idx))
		prop := i.a.values[i.idx]
		i.idx++
		if prop != nil {
			return propIterItem{name: name, value: prop}, i.next
		}
	}
	if !i.lengthDone {
		i.lengthDone = true
		return propIterItem{name: asciiString("length"), value: &i.a.lengthProp}, i.next
	}

	return i.a.baseObject.iterateStringKeys()()
}
```

In `array_sparse.go`, in the sparse→dense conversion, replace `a._put("length", &ar.lengthProp)` with:
```go
			// The dense object shares this map by value-copy; drop the sparse
			// object's length entry so the dense object's virtual length is
			// the only one.
			ar._delete("length")
```

- [ ] **Step 5: Run both tests, race, 386**

Run:
```bash
cd /home/lk/Work/goja && go test -run 'TestArrayLengthCharacterization|TestArrayLengthNotInMap|TestLazyPropertyMap' -v . 2>&1 | grep -E '^(--- |ok|FAIL|panic)' && go test -race -run 'TestArrayLength' . 2>&1 | tail -1 && GOARCH=386 go test -run 'TestArrayLength' . 2>&1 | tail -1
```
Expected: three `--- PASS` (the characterization test passes AFTER the change too — that is the equivalence proof); race `ok`; 386 `ok`.

- [ ] **Step 6: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkArrayLiteralCreate$' -benchmem -count=10 . > /tmp/goja-bench/b3-task2.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/b3-task2-base.txt /tmp/goja-bench/b3-task2.txt
```
Acceptance: allocs/op decreases by at least 2000 (map + map group per array) and the result is ≤ 2100 (Object + arrayObject per array plus a small constant). If not, STOP and report.

- [ ] **Step 7: Full suite and static checks**

Run (timeout 600000 ms): `cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6`
Expected: clean; all `ok` including test262.

- [ ] **Step 8: Commit**

```bash
cd /home/lk/Work/goja && git add array.go array_sparse.go lazy_props_test.go && git commit -F - <<'MSG'
Keep dense array length out of the property map

length already had dedicated get/set/define paths; the map entry existed
only for enumeration order and delete. Those now special-case it too,
yielding the lengthProp field's pointer so Object.freeze keeps mutating
the real storage, and a dense array no longer allocates a map at all.
Sparse arrays keep their entry; the sparse->dense conversion drops it.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 3: Function `length`/`name` without a map entry (spec §5)

**Files:**
- Modify: `func.go` — `baseFuncObject` struct (~line 60), `init` (~line 513), `funcObject` overrides (~lines 182–245); add `baseFuncObject` property-method overrides
- Test: `lazy_props_test.go` (append)

**Interfaces:**
- Consumes: `ensureValues` (Task 1).
- Produces: `baseFuncObject.intrinsicsVirtual bool`, `nameProp valueProperty`, `func (f *baseFuncObject) materializeIntrinsics()`.

- [ ] **Step 1: Add the benchmarks and capture the baseline**

Append to `lazy_props_test.go`:

```go
func BenchmarkFunctionCreate(b *testing.B) {
	runBenchClosure(b, `(function() { var n = 0; for (var i = 0; i < 1000; i++) { var f = function() { return i; }; n += f.length; } return n; })`)
}

// Object-literal methods get their name via defineOwnPropertyStr right after
// creation; that path must stay allocation-free for the intrinsics.
func BenchmarkMethodLiteralCreate(b *testing.B) {
	runBenchClosure(b, `(function() { var n = 0; for (var i = 0; i < 1000; i++) { var o = { m() { return i; } }; n += o.m.length; } return n; })`)
}
```

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkFunctionCreate$|BenchmarkMethodLiteralCreate$' -benchmem -count=10 . > /tmp/goja-bench/b3-task3-base.txt 2>&1 && grep -E "^Benchmark" /tmp/goja-bench/b3-task3-base.txt | sort -u -k1,1
```
Expected: two rows; record allocs/op (functions ≈ 5000+: Object, funcObject, map, group, name valueProperty; methods higher — the object literal too).

- [ ] **Step 2: Write the characterization test and confirm it passes on the unmodified engine**

Append to `lazy_props_test.go`:

```go
const funcIntrinsicsGoldenScript = `
function names(f) { return Object.getOwnPropertyNames(f).join(","); }
var out = [];
function decl(a, b) {}
var expr = function(a) {};
var arrow = (a, b, c) => 0;
var obj = { m(a) {}, get g() { return 1; } };
class K2 { static s(a, b) {} m() {} }
var bound = decl.bind(null, 1);
function* gen(a) {}
async function asy(a, b) {}
var native = Array.prototype.push;
var fns = [decl, expr, arrow, obj.m, K2, K2.s, K2.prototype.m, bound, gen, asy, native, Object.getOwnPropertyDescriptor(obj, "g").get];
for (var i = 0; i < fns.length; i++) {
	var f = fns[i];
	out.push(names(f) + ":" + f.name + ":" + f.length + ":" + JSON.stringify(Object.getOwnPropertyDescriptor(f, "name")) + ":" + JSON.stringify(Object.getOwnPropertyDescriptor(f, "length")));
}
function p1() {} p1.prototype; p1.u = 1; out.push(names(p1));
function p2() {} p2.u = 1; p2.prototype; out.push(names(p2));
function d1() {} out.push(delete d1.name, names(d1), d1.name, Object.prototype.hasOwnProperty.call(d1, "name"));
function d2() {} Object.defineProperty(d2, "name", { value: "renamed" }); out.push(names(d2), d2.name, JSON.stringify(Object.getOwnPropertyDescriptor(d2, "name")));
function d3() {} d3.u = 1; Object.defineProperty(d3, "name", { value: "w", writable: true, enumerable: true, configurable: true }); out.push(names(d3), Object.keys(d3).join(","), d3.name);
function d4() {} d4.name = "x"; out.push(d4.name);
out.push((function() { "use strict"; function d5() {} try { d5.name = "x"; return "no"; } catch (e) { return e.constructor.name; } })());
function d6() {} Object.freeze(d6); out.push(Object.isFrozen(d6), JSON.stringify(Object.getOwnPropertyDescriptor(d6, "name")), JSON.stringify(Object.getOwnPropertyDescriptor(d6, "length")));
class K3 { static name() { return "N"; } } out.push(typeof K3.name, names(K3));
function d7() {} d7[5] = 1; d7.u = 2; out.push(names(d7));
function d8() {} Object.defineProperty(d8, "length", { get() { return 42; } }); out.push(d8.length, names(d8));
function d9() {} out.push(Object.keys(d9).length, JSON.stringify(d9), d9.hasOwnProperty("length"), d9.hasOwnProperty("name"), d9.hasOwnProperty("x"));
out.join("|");
`

// Captured from the unmodified engine at 43617bb. Must pass before and after Task 3.
const funcIntrinsicsGolden = `prototype,length,name:decl:2:{"value":"decl","writable":false,"enumerable":false,"configurable":true}:{"value":2,"writable":false,"enumerable":false,"configurable":true}|prototype,length,name:expr:1:{"value":"expr","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name:arrow:3:{"value":"arrow","writable":false,"enumerable":false,"configurable":true}:{"value":3,"writable":false,"enumerable":false,"configurable":true}|length,name:m:1:{"value":"m","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name,prototype,s:K2:0:{"value":"K2","writable":false,"enumerable":false,"configurable":true}:{"value":0,"writable":false,"enumerable":false,"configurable":true}|length,name:s:2:{"value":"s","writable":false,"enumerable":false,"configurable":true}:{"value":2,"writable":false,"enumerable":false,"configurable":true}|length,name:m:0:{"value":"m","writable":false,"enumerable":false,"configurable":true}:{"value":0,"writable":false,"enumerable":false,"configurable":true}|length,name:bound decl:1:{"value":"bound decl","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name,prototype:gen:1:{"value":"gen","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name:asy:2:{"value":"asy","writable":false,"enumerable":false,"configurable":true}:{"value":2,"writable":false,"enumerable":false,"configurable":true}|length,name:push:1:{"value":"push","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name:get g:0:{"value":"get g","writable":false,"enumerable":false,"configurable":true}:{"value":0,"writable":false,"enumerable":false,"configurable":true}|length,name,prototype,u|length,name,u,prototype|true|prototype,length||false|prototype,length,name|renamed|{"value":"renamed","writable":false,"enumerable":false,"configurable":true}|prototype,length,name,u|name,u|w|d4|TypeError|true|{"value":"d6","writable":false,"enumerable":false,"configurable":false}|{"value":0,"writable":false,"enumerable":false,"configurable":false}|function|length,name,prototype|prototype,5,length,name,u|42|prototype,length,name|0||true|true|false`

func TestFuncIntrinsicsCharacterization(t *testing.T) {
	testScript(funcIntrinsicsGoldenScript, asciiString(funcIntrinsicsGolden), t)
}
```

Run: `cd /home/lk/Work/goja && go test -run 'TestFuncIntrinsicsCharacterization' -v . 2>&1 | tail -3`
Expected: `--- PASS` on the unmodified engine. Do not proceed until it does.

- [ ] **Step 3: Write the failing invariant test**

Append to `lazy_props_test.go`:

```go
func funcImpl(t *testing.T, r *Runtime, src string) *baseFuncObject {
	t.Helper()
	v, err := r.RunString(src)
	if err != nil {
		t.Fatal(err)
	}
	switch f := v.(*Object).self.(type) {
	case *funcObject:
		return &f.baseFuncObject
	case *arrowFuncObject:
		return &f.baseFuncObject
	case *methodFuncObject:
		return &f.baseFuncObject
	case *classFuncObject:
		return &f.baseFuncObject
	case *nativeFuncObject:
		return &f.baseFuncObject
	case *boundFuncObject:
		return &f.baseFuncObject
	case *generatorFuncObject:
		return &f.baseFuncObject
	case *asyncFuncObject:
		return &f.baseFuncObject
	default:
		t.Fatalf("unexpected function impl %T", f)
		return nil
	}
}

func TestFuncIntrinsicsVirtual(t *testing.T) {
	r := New()
	virtual := []string{
		`(function f(a) {})`,
		`(() => 0)`,
		`({ m() {} }).m`,
		`Array.prototype.push`,
		`(function() { function f() {} f.u = 1; return f; })()`,
		`(function() { function f() {} Object.defineProperty(f, "name", { value: "renamed" }); return f; })()`,
		`(function() { class K { static name() {} } return K; })()`,
		`(function() { function f() {} return f.bind(null); })()`,
		`(function*() {})`,
		`(async function() {})`,
	}
	for _, src := range virtual {
		f := funcImpl(t, r, src)
		if !f.intrinsicsVirtual {
			t.Errorf("%s: intrinsics unexpectedly materialized", src)
		}
		if _, ok := f.values["length"]; ok {
			t.Errorf("%s: length is in the property map", src)
		}
	}
	materialized := []string{
		`(function() { function f() {} delete f.name; return f; })()`,
		`(function() { function f() {} Object.defineProperty(f, "name", { value: "w", writable: true, enumerable: true, configurable: true }); return f; })()`,
	}
	for _, src := range materialized {
		f := funcImpl(t, r, src)
		if f.intrinsicsVirtual {
			t.Errorf("%s: expected intrinsics to be materialized", src)
		}
	}
	// Materialization places length, name at the front of propNames.
	f := funcImpl(t, r, `(function() { function f() {} f.u = 1; f.prototype; Object.defineProperty(f, "name", { value: "w", writable: true, enumerable: true, configurable: true }); return f; })()`)
	if len(f.propNames) < 2 || f.propNames[0] != "length" || f.propNames[1] != "name" {
		t.Errorf("propNames after materialization = %v, want length, name first", f.propNames)
	}
}
```

Run: `cd /home/lk/Work/goja && go test -run 'TestFuncIntrinsicsVirtual' . 2>&1 | tail -4`
Expected: compile error `f.intrinsicsVirtual undefined`.

- [ ] **Step 4: Implement the fields, init and materialization**

In `func.go`, replace the `baseFuncObject` struct and `init`:

```go
type baseFuncObject struct {
	baseObject

	lenProp  valueProperty
	nameProp valueProperty
	// intrinsicsVirtual is true while length and name live only in the
	// fields above. The first define or delete of either materializes both
	// into values/propNames (see materializeIntrinsics) and clears it.
	intrinsicsVirtual bool
}
```

```go
func (f *baseFuncObject) init(name unistring.String, length Value) {
	f.baseObject.init()

	f.lenProp.configurable = true
	f.lenProp.value = length

	f.nameProp.configurable = true
	f.nameProp.value = stringValueFromRaw(name)

	f.intrinsicsVirtual = true
}

// materializeIntrinsics moves length and name into the property map, at
// the front of propNames where init used to put them, so enumeration order
// is unchanged whether or not the intrinsics were ever materialized.
func (f *baseFuncObject) materializeIntrinsics() {
	if !f.intrinsicsVirtual {
		return
	}
	f.intrinsicsVirtual = false
	f.ensureValues()
	f.values["length"] = &f.lenProp
	f.values["name"] = &f.nameProp
	names := copyNamesIfNeeded(f.propNames, 2)
	names = append(names, "", "")
	copy(names[2:], names[:len(names)-2])
	names[0] = "length"
	names[1] = "name"
	f.propNames = names
	// Integer-like keys must still sort first; force fixPropOrder to
	// re-scan from the beginning on the next enumeration.
	f.lastSortedPropLen = 0
	f.idxPropCount = 0
}

func (f *baseFuncObject) intrinsicProp(name unistring.String) *valueProperty {
	if !f.intrinsicsVirtual {
		return nil
	}
	switch name {
	case "length":
		return &f.lenProp
	case "name":
		return &f.nameProp
	}
	return nil
}
```

- [ ] **Step 5: Add the `baseFuncObject` property-method overrides**

Append to `func.go` (after `materializeIntrinsics`):

```go
func (f *baseFuncObject) getStr(p unistring.String, receiver Value) Value {
	return f.getStrWithOwnProp(f.getOwnPropStr(p), p, receiver)
}

func (f *baseFuncObject) getOwnPropStr(name unistring.String) Value {
	if p := f.intrinsicProp(name); p != nil {
		return p
	}
	return f.baseObject.getOwnPropStr(name)
}

func (f *baseFuncObject) hasOwnPropertyStr(name unistring.String) bool {
	if f.intrinsicProp(name) != nil {
		return true
	}
	return f.baseObject.hasOwnPropertyStr(name)
}

func (f *baseFuncObject) setOwnStr(name unistring.String, val Value, throw bool) bool {
	if p := f.intrinsicProp(name); p != nil {
		if p.accessor {
			f.materializeIntrinsics()
			return f.baseObject.setOwnStr(name, val, throw)
		}
		f.val.runtime.typeErrorResult(throw, "Cannot assign to read only property '%s'", name)
		return false
	}
	return f.baseObject.setOwnStr(name, val, throw)
}

func (f *baseFuncObject) setForeignStr(name unistring.String, val, receiver Value, throw bool) (bool, bool) {
	return f._setForeignStr(name, f.getOwnPropStr(name), val, receiver, throw)
}

func (f *baseFuncObject) defineOwnPropertyStr(name unistring.String, descr PropertyDescriptor, throw bool) bool {
	if p := f.intrinsicProp(name); p != nil {
		v, ok := f._defineOwnProperty(name, p, descr, throw)
		if !ok {
			return false
		}
		if v == Value(p) {
			// Updated in place (the SetFunctionName case and every other
			// descriptor except the all-true data form): still virtual.
			return true
		}
		f.materializeIntrinsics()
		f.values[name] = v
		return true
	}
	return f.baseObject.defineOwnPropertyStr(name, descr, throw)
}

func (f *baseFuncObject) deleteStr(name unistring.String, throw bool) bool {
	if f.intrinsicProp(name) != nil {
		f.materializeIntrinsics()
	}
	return f.baseObject.deleteStr(name, throw)
}

func (f *baseFuncObject) stringKeys(all bool, accum []Value) []Value {
	if all && f.intrinsicsVirtual {
		accum = append(accum, asciiString("length"), asciiString("name"))
	}
	return f.baseObject.stringKeys(all, accum)
}

type funcIntrinsicsIter struct {
	f    *baseFuncObject
	idx  int
	rest iterNextFunc
}

func (i *funcIntrinsicsIter) next() (propIterItem, iterNextFunc) {
	switch i.idx {
	case 0:
		i.idx++
		return propIterItem{name: asciiString("length"), value: &i.f.lenProp}, i.next
	case 1:
		i.idx++
		return propIterItem{name: asciiString("name"), value: &i.f.nameProp}, i.next
	}
	return i.rest()
}

func (f *baseFuncObject) iterateStringKeys() iterNextFunc {
	if f.intrinsicsVirtual {
		return (&funcIntrinsicsIter{f: f, rest: f.baseObject.iterateStringKeys()}).next
	}
	return f.baseObject.iterateStringKeys()
}
```

Note on `setOwnStr`: a virtual intrinsic is non-writable unless a define converted it to an accessor in place; the accessor case is routed through the generic path after materializing so setter invocation semantics stay in one place.

- [ ] **Step 6: Re-point `funcObject`'s overrides to `baseFuncObject`**

In `func.go`, in the `funcObject` methods, change every `f.baseObject.` delegation to `f.baseFuncObject.` in: `getOwnPropStr`, `setOwnStr`, `defineOwnPropertyStr`, `deleteStr`, `hasOwnPropertyStr`. (`stringKeys` and `iterateStringKeys` already delegate to `baseFuncObject`; `getStr` and `setForeignStr` resolve through `f.getOwnPropStr`, which is `funcObject`'s own and now chains correctly.) Then confirm no other function type overrides these methods:

```bash
cd /home/lk/Work/goja && grep -n "^func (f \*[A-Za-z]*) \(getOwnPropStr\|hasOwnPropertyStr\|deleteStr\|defineOwnPropertyStr\|setOwnStr\|setForeignStr\|stringKeys\|iterateStringKeys\|getStr\)(" *.go | grep -v _test | grep -v "baseFuncObject\|funcObject)" 
```
Expected: no output. If a type other than `funcObject`/`baseFuncObject` appears, STOP and report it (it would bypass the virtual checks).

- [ ] **Step 7: Run the tests, race, 386**

Run:
```bash
cd /home/lk/Work/goja && go test -run 'TestFuncIntrinsics|TestArrayLength|TestLazyPropertyMap' -v . 2>&1 | grep -E '^(--- |ok|FAIL|panic)' && go test -race -run 'TestFuncIntrinsics' . 2>&1 | tail -1 && GOARCH=386 go test -run 'TestFuncIntrinsics' . 2>&1 | tail -1
```
Expected: all `--- PASS` — the characterization test passing after the change is the equivalence proof; race `ok`; 386 `ok`. If `TestFuncIntrinsicsCharacterization` fails after the change, report the differing segment (split on `|`) — do not edit the golden.

- [ ] **Step 8: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkFunctionCreate$|BenchmarkMethodLiteralCreate$' -benchmem -count=10 . > /tmp/goja-bench/b3-task3.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/b3-task3-base.txt /tmp/goja-bench/b3-task3.txt
```
Acceptance: both benchmarks' allocs/op decrease by at least 2000 (map + group per function; the `name` `valueProperty` is a third) and `MethodLiteralCreate`'s decrease is not smaller than `FunctionCreate`'s (proves the define-name path stayed virtual). If not, STOP and report.

- [ ] **Step 9: Full suite and static checks**

Run (timeout 600000 ms): `cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6`
Expected: clean; all `ok` including test262 (Function own-keys ordering, `freeze`, `defineProperty` on `name`/`length` are heavily tested there).

- [ ] **Step 10: Commit**

```bash
cd /home/lk/Work/goja && git add func.go lazy_props_test.go && git commit -F - <<'MSG'
Keep function length and name out of the property map until needed

baseFuncObject now holds both intrinsics in fields behind a single
intrinsicsVirtual flag and overrides the property methods to consult
them first. Defining name after creation (object-literal and class
methods, anonymous functions) updates the field in place; only a delete
or an all-true redefinition materializes both into the map, at the
front of propNames where init used to put them, so enumeration order is
unchanged. funcObject's overrides now chain through baseFuncObject.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 4: Eager regexp2 for global/sticky templates (spec §6)

**Files:**
- Modify: `builtin_regexp.go` — `compileRegexpUncached`, the wrapper-selection tail
- Test: `regexp_test.go` (append)

**Interfaces:** none new.

- [ ] **Step 1: Capture the baseline**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkMatchAllLoop$' -benchmem -count=10 . > /tmp/goja-bench/b3-task4-base.txt 2>&1 && grep -E "^Benchmark" /tmp/goja-bench/b3-task4-base.txt | head -1
```
Expected: one row (≈ 11k allocs/op). Record it.

- [ ] **Step 2: Write the failing test**

Append to `regexp_test.go`:

```go
func TestRegexpEagerRegexp2ForGlobal(t *testing.T) {
	resetRegexpCache()
	for _, flags := range []string{"g", "y", "gy", "gi", "gu"} {
		p, err := compileRegexp("a+", flags)
		if err != nil {
			t.Fatal(err)
		}
		if p.regexpWrapper == nil {
			t.Fatalf("%q: expected the Go regexp engine", flags)
		}
		if p.regexp2Wrapper == nil {
			t.Fatalf("%q: expected regexp2 to be built eagerly for a global/sticky pattern", flags)
		}
		q, err := compileRegexp("a+", flags)
		if err != nil {
			t.Fatal(err)
		}
		if q.regexp2Wrapper.rx != p.regexp2Wrapper.rx {
			t.Fatalf("%q: clones must share the eagerly built regexp2 engine", flags)
		}
	}
	p, err := compileRegexp("a+", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.regexp2Wrapper != nil {
		t.Fatal("a non-global, non-sticky pattern must not build regexp2 eagerly")
	}
	p, err = compileRegexp("(?<=a)b", "g")
	if err != nil {
		t.Fatal(err)
	}
	if p.regexpWrapper != nil || p.regexp2Wrapper == nil {
		t.Fatal("a pattern Go's regexp rejects must still take the regexp2-only path")
	}
	// Behaviour is unchanged across the matching paths that use lastIndex.
	testScript(`
		var out = [];
		out.push([..."a1b22c333".matchAll(/\d+/g)].map(function(m) { return m.index + ":" + m[0]; }).join(","));
		var re = /a/gy; out.push(re.exec("aab")[0], re.lastIndex, re.exec("aab")[0], re.lastIndex, re.exec("aab"), re.lastIndex);
		out.push([..."ab".matchAll(/(?:)/g)].map(function(m) { return m.index; }).join(","));
		out.push([..."x\u{1F600}y".matchAll(/./gu)].length);
		out.push("aXbXc".replace(/X/g, "-"), "a1b2".split(/\d/).join("|"));
		out.join("|");
	`, asciiString("1:1,3:22,6:333|a|1|a|2||0|0,1,2|3|a-b-c|a|b|"), t)
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd /home/lk/Work/goja && go test -run 'TestRegexpEagerRegexp2ForGlobal' . 2>&1 | tail -4`
Expected: FAIL with `expected regexp2 to be built eagerly for a global/sticky pattern`.

- [ ] **Step 4: Implement**

In `builtin_regexp.go`, `compileRegexpUncached`, replace the block that begins `if wrapper == nil {` and builds `wrapper2` with:

```go
	if wrapper == nil {
		wrapper2, err = compileRegexp2(patternStr, multiline, dotAll, ignoreCase, unicode)
		if err != nil {
			err = fmt.Errorf("Invalid regular expression (regexp2): %s (%v)", patternStr, err)
			return
		}
	} else if global || sticky {
		// Matching from lastIndex > 0 needs regexp2 (Go's regexp cannot start
		// mid-string with correct anchors). Build it once here, on the
		// template, instead of lazily on every clone in createRegexp2.
		wrapper2, err = compileRegexp2(patternStr, multiline, dotAll, ignoreCase, unicode)
		if err != nil {
			err = fmt.Errorf("Invalid regular expression (regexp2): %s (%v)", patternStr, err)
			return
		}
	}
```

Leave `createRegexp2` in place as the safety net for any remaining lazy path.

- [ ] **Step 5: Run the test and race**

Run:
```bash
cd /home/lk/Work/goja && go test -run 'TestRegexpEagerRegexp2ForGlobal|TestRegexpCache' -v . 2>&1 | grep -E '^(--- |ok|FAIL|panic)' && go test -race -run 'TestRegexpEagerRegexp2ForGlobal|TestRegexpCacheConcurrent' . 2>&1 | tail -1
```
Expected: all `--- PASS`; race `ok`.

- [ ] **Step 6: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkMatchAllLoop$' -benchmem -count=10 . > /tmp/goja-bench/b3-task4.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/b3-task4-base.txt /tmp/goja-bench/b3-task4.txt
```
Acceptance: allocs/op decreases (the per-op regexp2 rebuild is gone) and does not increase. Report the delta.

- [ ] **Step 7: Full suite and static checks**

Run (timeout 600000 ms): `cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6`
Expected: clean; all `ok` including test262 (RegExp tests exercise both engines).

- [ ] **Step 8: Commit**

```bash
cd /home/lk/Work/goja && git add builtin_regexp.go regexp_test.go && git commit -F - <<'MSG'
Build the regexp2 engine eagerly for global and sticky patterns

Matching from lastIndex > 0 always needs regexp2, and createRegexp2 was
rebuilding it lazily on every clone. Build it once on the cached
template instead; clones share it. A regexp2 compile failure now
surfaces as a SyntaxError at compile time instead of a panic on first
use.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 5: `Keys` fast path and `AppendKeys` (spec §7)

**Files:**
- Modify: `value.go` — `func (o *Object) Keys()` (~line 861)
- Modify: `object.go` — add `appendEnumerableStringKeys` on `baseObject`
- Modify: `array.go` — add `appendEnumerableStringKeys` on `arrayObject`
- Test: `object_test.go` (append)

**Interfaces:**
- Produces: public `func (o *Object) AppendKeys(dst []string) []string`; internal `func (o *baseObject) appendEnumerableStringKeys(dst []string) []string` and `func (a *arrayObject) appendEnumerableStringKeys(dst []string) []string`.

- [ ] **Step 1: Add the benchmarks and capture the baseline**

Append to `object_test.go`:

```go
func BenchmarkObjectKeys(b *testing.B) {
	r := New()
	v, err := r.RunString(`({a: 1, b: 2, c: 3, d: 4, e: 5, f: 6, g: 7, h: 8, i: 9, j: 10})`)
	if err != nil {
		b.Fatal(err)
	}
	o := v.ToObject(r)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(o.Keys()) != 10 {
			b.Fatal("wrong key count")
		}
	}
}

func BenchmarkAppendKeysReused(b *testing.B) {
	r := New()
	v, err := r.RunString(`({a: 1, b: 2, c: 3, d: 4, e: 5, f: 6, g: 7, h: 8, i: 9, j: 10})`)
	if err != nil {
		b.Fatal(err)
	}
	o := v.ToObject(r)
	buf := make([]string, 0, 16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = o.AppendKeys(buf[:0])
		if len(buf) != 10 {
			b.Fatal("wrong key count")
		}
	}
}
```

`BenchmarkAppendKeysReused` will not compile yet; capture the `Keys` baseline with it stubbed:

```bash
cd /home/lk/Work/goja && sed -i 's/buf = o.AppendKeys(buf\[:0\])/buf = append(buf[:0], o.Keys()...) \/\/ BASELINE-STUB/' object_test.go && \
go test -run=NONE -bench='BenchmarkObjectKeys$' -benchmem -count=10 . > /tmp/goja-bench/b3-task5-base.txt 2>&1 ; \
sed -i 's/buf = append(buf\[:0\], o.Keys()...) \/\/ BASELINE-STUB/buf = o.AppendKeys(buf[:0])/' object_test.go && grep -c BASELINE-STUB object_test.go; grep -E "^Benchmark" /tmp/goja-bench/b3-task5-base.txt | head -1
```
Expected: `0` (stub removed); `Keys` shows ≈ 13+ allocs/op (3 iterator allocations + one boxed name per key + slice growth). Record it.

- [ ] **Step 2: Write the failing tests**

Append to `object_test.go`:

```go
// genericKeys is Keys as implemented before the fast path: the enumerable
// iterator over iterateStringKeys. It is the oracle the fast path must match.
func genericKeys(o *Object) (keys []string) {
	iter := &enumerableIter{o: o, wrapped: o.self.iterateStringKeys()}
	for item, next := iter.next(); next != nil; item, next = next() {
		keys = append(keys, item.name.String())
	}
	return
}

func TestAppendKeysMatchesGeneric(t *testing.T) {
	r := New()
	r.Set("goStruct", struct {
		A int
		B string
	}{1, "x"})
	r.Set("goMap", map[string]interface{}{"m1": 1, "m2": 2})
	sources := []string{
		`({})`,
		`({a: 1, b: 2, 5: "five", c: 3, 1: "one"})`,
		`(function() { var o = {a: 1, b: 2}; Object.defineProperty(o, "h", {value: 3, enumerable: false}); delete o.a; o.z = 4; return o; })()`,
		`[]`,
		`[1, , 3]`,
		`(function() { var a = [1, 2]; a.x = 1; Object.defineProperty(a, "1", {value: 9, enumerable: false}); a[5] = 5; return a; })()`,
		`(function() { var s = []; s[100000] = 1; s.y = 2; return s; })()`,
		`new Proxy({p: 1, q: 2}, { ownKeys(t) { return ["q", "p", "extra"]; }, getOwnPropertyDescriptor(t, k) { return k === "extra" ? {value: 0, enumerable: true, configurable: true} : Reflect.getOwnPropertyDescriptor(t, k); } })`,
		`goStruct`,
		`goMap`,
		`(function() { var f = function() {}; f.u = 1; return f; })()`,
		`globalThis`,
		`(function() { var s = new String("ab"); s.x = 1; return s; })()`,
		`({"é": 1, plain: 2})`,
	}
	for _, src := range sources {
		v, err := r.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		o := v.ToObject(r)
		want := genericKeys(o)
		got := o.Keys()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: Keys = %v, generic = %v", src, got, want)
		}
		if (got == nil) != (want == nil) {
			t.Errorf("%s: nil-ness differs: Keys=%v generic=%v", src, got == nil, want == nil)
		}
		buf := o.AppendKeys([]string{"pre"})
		if len(buf) == 0 || buf[0] != "pre" || strings.Join(buf[1:], ",") != strings.Join(want, ",") {
			t.Errorf("%s: AppendKeys = %v, want pre + %v", src, buf, want)
		}
	}
	// Mutation between calls is observed.
	v, err := r.RunString(`({a: 1})`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.ToObject(r)
	first := o.Keys()
	if err := o.Set("b", 2); err != nil {
		t.Fatal(err)
	}
	if got := o.Keys(); strings.Join(got, ",") != "a,b" || strings.Join(first, ",") != "a" {
		t.Errorf("mutation not observed: first=%v second=%v", first, got)
	}
}

func TestAppendKeysAllocationFree(t *testing.T) {
	r := New()
	v, err := r.RunString(`({a: 1, b: 2, c: 3})`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.ToObject(r)
	buf := make([]string, 0, 8)
	if allocs := testing.AllocsPerRun(200, func() { buf = o.AppendKeys(buf[:0]) }); allocs != 0 {
		t.Fatalf("AppendKeys into a reused buffer allocated %v times, want 0", allocs)
	}
	if allocs := testing.AllocsPerRun(200, func() { _ = o.Keys() }); allocs != 1 {
		t.Fatalf("Keys on a plain object allocated %v times, want exactly 1", allocs)
	}
	av, err := r.RunString(`[1, 2, 3]`)
	if err != nil {
		t.Fatal(err)
	}
	arr := av.ToObject(r)
	if allocs := testing.AllocsPerRun(200, func() { buf = arr.AppendKeys(buf[:0]) }); allocs != 0 {
		t.Fatalf("AppendKeys on a small array allocated %v times, want 0", allocs)
	}
}
```

`object_test.go` already imports `strings` and `testing`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd /home/lk/Work/goja && go test -run 'TestAppendKeys' . 2>&1 | tail -4`
Expected: compile error `o.AppendKeys undefined`.

- [ ] **Step 4: Implement the internal walkers**

In `object.go`, after `stringKeys`:

```go
// appendEnumerableStringKeys appends the object's own enumerable string
// keys in spec order without iterator state or boxed names. It runs no
// user code, so propNames cannot change underneath it.
func (o *baseObject) appendEnumerableStringKeys(dst []string) []string {
	o.ensurePropOrder()
	for _, k := range o.propNames {
		prop := o.values[k]
		if prop == nil {
			continue
		}
		if p, ok := prop.(*valueProperty); ok && !p.enumerable {
			continue
		}
		if dst == nil {
			dst = make([]string, 0, len(o.propNames))
		}
		dst = append(dst, k.String())
	}
	return dst
}
```

In `array.go`, after `stringKeys`:

```go
func (a *arrayObject) appendEnumerableStringKeys(dst []string) []string {
	for i, prop := range a.values {
		if prop == nil {
			continue
		}
		if p, ok := prop.(*valueProperty); ok && !p.enumerable {
			continue
		}
		if dst == nil {
			dst = make([]string, 0, len(a.values)+len(a.propNames))
		}
		dst = append(dst, strconv.Itoa(i))
	}
	return a.baseObject.appendEnumerableStringKeys(dst)
}
```

- [ ] **Step 5: Implement `AppendKeys` and re-route `Keys`**

In `value.go`, replace `func (o *Object) Keys() (keys []string)` with:

```go
// Keys returns a list of Object's enumerable keys.
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) Keys() []string {
	return o.AppendKeys(nil)
}

// AppendKeys appends the object's own enumerable string keys, in the same
// order Keys returns them, and returns the extended slice. Passing a slice
// with spare capacity makes the call allocation-free for ordinary objects
// and arrays; other objects (Proxy, Go-backed, templated) fall back to the
// generic enumeration and allocate as Keys does.
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) AppendKeys(dst []string) []string {
	switch impl := o.self.(type) {
	case *baseObject:
		return impl.appendEnumerableStringKeys(dst)
	case *arrayObject:
		return impl.appendEnumerableStringKeys(dst)
	}
	iter := &enumerableIter{
		o:       o,
		wrapped: o.self.iterateStringKeys(),
	}
	for item, next := iter.next(); next != nil; item, next = next() {
		dst = append(dst, item.name.String())
	}
	return dst
}
```

- [ ] **Step 6: Run the tests and race**

Run:
```bash
cd /home/lk/Work/goja && go test -run 'TestAppendKeys' -v . 2>&1 | grep -E '^(--- |ok|FAIL|panic)' && go test -race -run 'TestAppendKeysMatchesGeneric' . 2>&1 | tail -1
```
Expected: two `--- PASS`; race `ok`. If `TestAppendKeysMatchesGeneric` reports a difference for a source, the fast path's enumerability/ordering predicate diverges from `enumerableIter` for that case — fix the walker, never the oracle.

- [ ] **Step 7: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkObjectKeys$|BenchmarkAppendKeysReused$' -benchmem -count=10 . > /tmp/goja-bench/b3-task5.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/b3-task5-base.txt /tmp/goja-bench/b3-task5.txt; grep -E "^BenchmarkAppendKeysReused" /tmp/goja-bench/b3-task5.txt | head -1
```
Acceptance: `BenchmarkObjectKeys` = exactly 1 allocs/op; `BenchmarkAppendKeysReused` = 0 allocs/op.

- [ ] **Step 8: Full suite and static checks**

Run (timeout 600000 ms): `cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6`
Expected: clean; all `ok`.

- [ ] **Step 9: Commit**

```bash
cd /home/lk/Work/goja && git add value.go object.go array.go object_test.go && git commit -F - <<'MSG'
Add Object.AppendKeys and an allocation-free path for Keys

Keys built iterator state and boxed every name; for ordinary objects and
dense arrays it now walks propNames directly, allocating only the result
slice. AppendKeys exposes the same walk into a caller-supplied buffer for
zero-allocation enumeration from Go. Exotic objects keep the generic
iterator.

<paste benchmark rows here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 6: Final verification and hand-off

**Files:**
- Modify: `docs/superpowers/specs/2026-09-16-object-model-batch3-design.md` (status line)

- [ ] **Step 1: Clean-tree verification**

Run (timeout 600000 ms):
```bash
cd /home/lk/Work/goja && git status --porcelain && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6 && go test -race -count=1 -run 'TestLazyPropertyMap|TestArrayLength|TestFuncIntrinsics|TestRegexpEagerRegexp2ForGlobal|TestAppendKeys' . 2>&1 | tail -1 && GOARCH=386 go test -count=1 -run 'TestLazyPropertyMap|TestArrayLength|TestFuncIntrinsics|TestAppendKeys' . 2>&1 | tail -1
```
Expected: nothing from `git status`; clean; all `ok`; race `ok`; 386 `ok`.

- [ ] **Step 2: Whole-branch benchmark summary**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkArrayIterObjectCreate$|BenchmarkArrayLiteralCreate$|BenchmarkFunctionCreate$|BenchmarkMethodLiteralCreate$|BenchmarkMatchAllLoop$|BenchmarkObjectKeys$|BenchmarkAppendKeysReused$|BenchmarkForOfArray$|BenchmarkCallJS$|BenchmarkFib$|BenchmarkVMAdd$' -benchmem -count=10 . 2>&1 | grep -E "^Benchmark" | sort -k1,1 | awk '{k=$1; a[k]=$0} END{for (k in a) print a[k]}' | sort | tee /tmp/goja-bench/b3-summary.txt
```
Expected: one row per benchmark; sentinels (`ForOfArray` 4, `CallJS` 0, `VMAdd` 0, `Fib` 278 allocs/op) unchanged from batch 2's summary.

- [ ] **Step 3: Update the spec and commit**

Change the spec's `**Status:**` line to `implemented on branch perf/object-model-batch3 (2026-09-16); per-change benchmarks in commit messages`, then:

```bash
cd /home/lk/Work/goja && git add docs/superpowers/specs/2026-09-16-object-model-batch3-design.md && git commit -q -m "Mark object model batch 3 spec as implemented

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>" && git log --oneline master..HEAD
```
Expected: eight commits (spec, plan, five changes, status update).

- [ ] **Step 4: Report**

Report the commit list, head SHA and `/tmp/goja-bench/b3-summary.txt`. Do **not** push and do **not** merge; the controller pushes to the fork after confirmation and merges after the user's GAP qualification.
