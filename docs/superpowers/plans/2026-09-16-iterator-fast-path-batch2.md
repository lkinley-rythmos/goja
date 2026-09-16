# Iterator Fast Path Batch 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the allocation sources the GAP workload profile identified — iterator-protocol result objects, runtime regex recompilation, and bridge descriptor objects — as four independent, individually measured engine changes.

**Architecture:** `getIterator` verifies once per iteration that the iterator is an intrinsic implementation paired with the runtime's own intrinsic `next` object and, if so, records a Go-level `nextValue()` on the `iteratorRecord`; `step`/`iterate` — the only two consumers — call it instead of materializing `{value, done}`. The intrinsic `next()` methods are refactored so one body serves both paths. A public `GetOwnDataValue` reads an own data property without a descriptor object. `compileRegexp` gains a bounded process-global template cache that hands out `clone()`s, mirroring what the compiler already does for literals.

**Tech Stack:** Go 1.25 (module floor), `testing` benchmarks with `-benchmem`, `benchstat` (`~/go/bin/benchstat`), tc39 test262 under `testdata/test262`.

**Spec:** `docs/superpowers/specs/2026-09-16-iterator-fast-path-batch2-design.md`

## Global Constraints

- Go floor is `go 1.25.0` (from `go.mod`).
- All work on branch `perf/iterator-fast-path-batch2` (exists; spec committed at `ce51c94`, branched from `master`).
- Every commit must pass: `gofmt -l .` (empty output), `go vet ./...`, `go test ./...` **with test262 checked out** (`testdata/test262` is present from batch 1; Task 0 verifies), and `go test -race` on the tests each task adds.
- Tests and benchmarks go in goja's own `*_test.go` files (package `goja`, internal access allowed). Never commit anything under `testdata/test262`.
- The only public API addition is `func (o *Object) GetOwnDataValue(name string) (Value, bool)`. No new dependencies.
- Commit messages end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` — exactly this line, regardless of the implementer's own model name.
- Benchmark acceptance is judged on `allocs/op` (deterministic): each task captures its own baseline **before** implementing (the benchmark is added first) and states the exact acceptance rule. Time deltas are directional (the machine throttles; a task is not rejected for a noisy time delta if the allocation rule holds and nothing regresses beyond noise). Expected figures are measured on this codebase, never carried over from a spike.
- Spec §3.6 rule for every `nextValue()`: identical reads in identical order to the corresponding `next()`; `next()` must be implemented as `nextValue()` + `createIterResultObject`, never a second copy.
- No change to `rcx-go-crud`; the app-side patch is spec §8 and is the user's to apply.

---

### Task 0: Prerequisites

**Files:** none modified.

**Interfaces:**
- Produces: confirmation that the branch base is green with test262 running.

- [ ] **Step 1: Confirm branch, clean tree, and test262 presence**

Run:
```bash
cd /home/lk/Work/goja && git branch --show-current && git status --porcelain | wc -l && ls testdata/test262/test | head -3
```
Expected: `perf/iterator-fast-path-batch2`, `0`, and a listing (e.g. `annexB built-ins ...`). If `testdata/test262` is missing, run `./.tc39_test262_checkout.sh` first.

- [ ] **Step 2: Confirm the suite is green on the branch base**

Run (timeout 600000 ms):
```bash
cd /home/lk/Work/goja && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6
```
Expected: every package `ok`; the root package run takes > 10 s (test262 executing). If it prints `If you want to run tc39 tests, download them ...`, stop — test262 is not being run.

---

### Task 1: Intrinsic iterator fast path — mechanism + array iterator (spec §3)

**Files:**
- Modify: `runtime.go` — `global` struct (near the `ArrayIteratorPrototype` field, ~line 128); `iteratorRecord`, `getIterator`, `iterate`, `step`, `returnIter`, `close` (~lines 2707–2800, locate by content)
- Modify: `builtin_array.go` — `createArrayIterProto` (~line 1714)
- Modify: `array.go` — `arrayIterObject.next` (~lines 20–45)
- Create: `iterator_fastpath_test.go`

**Interfaces:**
- Produces: `type iterNexter interface { nextValue() (value Value, done bool) }`; field `iteratorRecord.fast iterNexter`; `func (r *Runtime) intrinsicIterNexter(iter, nextObj *Object) iterNexter`; `r.global.arrayIterProtoNext *Object` and `r.global.regExpStringIterProtoNext *Object` (the latter declared here, populated in Task 2); `func (ai *arrayIterObject) nextValue() (Value, bool)`; test helper `fastPathEngaged(t, r, src) bool` and benchmark helper `benchCallable(b, setup) (Callable, *Runtime)` in `iterator_fastpath_test.go`.

- [ ] **Step 1: Create the test file with helpers and benchmarks only (so a baseline can be captured against the unmodified engine)**

Create `iterator_fastpath_test.go`:

```go
package goja

import (
	"testing"
)

// fastPathEngaged evaluates src, which must yield an iterable, and reports
// whether getIterator recorded a Go-level nexter for it.
func fastPathEngaged(t *testing.T, r *Runtime, src string) bool {
	t.Helper()
	v, err := r.RunString(src)
	if err != nil {
		t.Fatal(err)
	}
	return r.getIterator(v, nil).fast != nil
}

// benchCallable evaluates setup, which must yield a zero-argument function,
// and returns it as a Callable so the benchmark loop measures only the call.
func benchCallable(b *testing.B, setup string) (Callable, *Runtime) {
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
	return f, r
}

func runBench(b *testing.B, setup string) {
	f, _ := benchCallable(b, setup)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f(nil); err != nil {
			b.Fatal(err)
		}
	}
}

const benchArraySetup = `var a = []; for (var i = 0; i < 1000; i++) a.push(i);`

func BenchmarkForOfArray(b *testing.B) {
	runBench(b, benchArraySetup+`(function() { var n = 0; for (var v of a) n++; return n; })`)
}

func BenchmarkArraySpread(b *testing.B) {
	runBench(b, benchArraySetup+`(function() { return [...a].length; })`)
}

func BenchmarkArrayDestructure(b *testing.B) {
	runBench(b, benchArraySetup+`(function() { var t = 0; for (var i = 0; i < 100; i++) { var [x, y, z] = a; t += x + y + z; } return t; })`)
}

func BenchmarkArrayFrom(b *testing.B) {
	runBench(b, benchArraySetup+`(function() { return Array.from(a).length; })`)
}
```

Note: `fastPathEngaged` references `.fast`, which does not exist yet — that is intentional; the file must NOT compile until Step 5. Capture the baseline with the helper temporarily excluded: run Step 2 exactly as written.

- [ ] **Step 2: Capture the baseline allocation numbers on the unmodified engine**

Temporarily rename the not-yet-compilable helper so the benchmarks build, capture, then restore:

```bash
cd /home/lk/Work/goja && sed -i 's/return r.getIterator(v, nil).fast != nil/return false \/\/ BASELINE-STUB/' iterator_fastpath_test.go && \
go test -run=NONE -bench='BenchmarkForOfArray$|BenchmarkArraySpread$|BenchmarkArrayDestructure$|BenchmarkArrayFrom$' -benchmem -count=10 . > /tmp/goja-bench/b2-task1-base.txt 2>&1 ; \
sed -i 's/return false \/\/ BASELINE-STUB/return r.getIterator(v, nil).fast != nil/' iterator_fastpath_test.go && grep -c "BASELINE-STUB" iterator_fastpath_test.go; grep -E "^Benchmark" /tmp/goja-bench/b2-task1-base.txt | awk '{print $1, $NF, $(NF-1), $(NF-2)}' | sort -u | head
```
Expected: `mkdir -p /tmp/goja-bench` first if it does not exist. The grep count prints `0` (stub removed). Record the four baseline `allocs/op` values in your report — they should each be in the hundreds to thousands (one result object per iteration step).

- [ ] **Step 3: Write the failing tests**

Append to `iterator_fastpath_test.go`:

```go
func TestIteratorFastPathGate(t *testing.T) {
	engaged := []string{
		`[1, 2, 3]`,
		`[1, , 3]`,
		`new Uint8Array([1, 2])`,
		`[1, 2].keys()`,
		`[1, 2].entries()`,
		`Array.prototype.values.call({length: 2, 0: "a", 1: "b"})`,
	}
	for _, src := range engaged {
		if !fastPathEngaged(t, New(), src) {
			t.Errorf("expected fast path for %s", src)
		}
	}
	notEngaged := []string{
		`new Map([[1, 2]])`,
		`new Set([1])`,
		`new Map([[1, 2]]).keys()`,
		`"abc"`,
		`(function*() { yield 1; })()`,
		`({ [Symbol.iterator]() { return { next() { return { done: true }; } }; } })`,
		`(function() { var a = [1]; a[Symbol.iterator] = function() { return { next() { return { done: true }; } }; }; return a; })()`,
		`(function() { var it = [1][Symbol.iterator](); it.next = function() { return { done: true }; }; return it; })()`,
	}
	for _, src := range notEngaged {
		if fastPathEngaged(t, New(), src) {
			t.Errorf("expected generic path for %s", src)
		}
	}

	// Replacing %ArrayIteratorPrototype%.next disables the fast path for every array in that runtime.
	r := New()
	if !fastPathEngaged(t, r, `[1]`) {
		t.Fatal("precondition: fast path engaged on a fresh runtime")
	}
	if _, err := r.RunString(`var proto = Object.getPrototypeOf([][Symbol.iterator]()); var origNext = proto.next; proto.next = function() { return origNext.call(this); };`); err != nil {
		t.Fatal(err)
	}
	if fastPathEngaged(t, r, `[1]`) {
		t.Fatal("replaced %ArrayIteratorPrototype%.next must disable the fast path")
	}

	// An accessor for next disables it too.
	r = New()
	if _, err := r.RunString(`Object.defineProperty(Object.getPrototypeOf([][Symbol.iterator]()), "next", { configurable: true, get() { return function() { return { done: true }; }; } });`); err != nil {
		t.Fatal(err)
	}
	if fastPathEngaged(t, r, `[1]`) {
		t.Fatal("accessor next must disable the fast path")
	}

	// Replacing Array.prototype[Symbol.iterator] disables it.
	r = New()
	if _, err := r.RunString(`Array.prototype[Symbol.iterator] = function() { return { next() { return { done: true }; } }; };`); err != nil {
		t.Fatal(err)
	}
	if fastPathEngaged(t, r, `[1]`) {
		t.Fatal("replaced Array.prototype[Symbol.iterator] must disable the fast path")
	}

	// An iterator created by another Runtime never matches this runtime's anchor.
	other := New()
	v, err := other.RunString(`[1, 2]`)
	if err != nil {
		t.Fatal(err)
	}
	if New().getIterator(v, nil).fast != nil {
		t.Fatal("cross-runtime iterator must not take the fast path")
	}
}

func TestIteratorFastPathObservable(t *testing.T) {
	// Every observable effect must be identical on both paths. Each script
	// runs once with the intrinsic next (fast path) and once after wrapping
	// %ArrayIteratorPrototype%.next (generic path); results must match.
	const wrapNext = `var __p = Object.getPrototypeOf([][Symbol.iterator]()); var __n = __p.next; __p.next = function() { return __n.call(this); };`
	cases := []struct {
		name, script, want string
	}{
		{"for-of/spread/destructure/from/map/fromEntries", `
			var out = [];
			var s = 0; for (var v of [1, 2, 3]) s += v; out.push(s);
			out.push([...[4, 5]].join("-"));
			var [a, b] = [6, 7, 8]; out.push(a + ":" + b);
			out.push(Array.from([1, , 3]).length, Array.from([1, , 3])[1] === undefined);
			out.push(new Map([[1, "x"], [2, "y"]]).get(2));
			out.push(Object.fromEntries([["k", 9]]).k);
			out.push([...[1, , 3]].length);
			var early = 0; for (var w of [1, 2, 3]) { early++; break; } out.push(early);
			out.join("|");`, "6|4-5|6:7|3|true|y|9|3|1"},
		{"throwing length getter surfaces identically", `
			var it = Array.prototype.values.call({ get length() { throw new Error("boom"); } });
			var msg = ""; try { for (var v of it) {} } catch (e) { msg = e.message; }
			msg;`, "boom"},
		{"proxy trap order", `
			var log = [];
			var p = new Proxy([10, 20], { get(t, k, r) { log.push(String(k)); return Reflect.get(t, k, r); } });
			var out = []; for (var v of p) out.push(v);
			log.join(",") + " => " + out.join(",");`, "Symbol(Symbol.iterator),length,0,length,1,length => 10,20"},
		{"direct next() still returns result objects", `
			var it = [1][Symbol.iterator](); var r1 = it.next(); var r2 = it.next(); var r3 = it.next();
			[r1.value, r1.done, r2.value, r2.done, r3.done].join(",");`, "1,false,,true,true"},
		{"incompatible receiver", `
			var msg = ""; try { Object.getPrototypeOf([][Symbol.iterator]()).next.call(new Map().keys()); } catch (e) { msg = e.constructor.name; } msg;`, "TypeError"},
	}
	for _, c := range cases {
		for _, generic := range []bool{false, true} {
			r := New()
			src := c.script
			if generic {
				src = wrapNext + src
			}
			v, err := r.RunString(src)
			if err != nil {
				t.Fatalf("%s (generic=%v): %v", c.name, generic, err)
			}
			if got := v.String(); got != c.want {
				t.Fatalf("%s (generic=%v): got %q, want %q", c.name, generic, got, c.want)
			}
		}
	}

	// Tampered next is called exactly once per step on the generic path.
	r := New()
	v, err := r.RunString(`
		var proto = Object.getPrototypeOf([][Symbol.iterator]()); var orig = proto.next; var calls = 0;
		proto.next = function() { calls++; return orig.call(this); };
		var s = 0; for (var v of [1, 2, 3]) s += v; var t = [...[4, 5]];
		calls + ":" + s + ":" + t.length;`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "7:6:2" {
		t.Fatalf("tampered next call count: got %q, want %q", got, "7:6:2")
	}
}

func TestIteratorFastPathAllocationFree(t *testing.T) {
	// Allocation per iteration must not scale with the number of elements:
	// iterating 10 and 1000 elements must cost the same.
	measure := func(n int) float64 {
		r := New()
		v, err := r.RunString(`var a = []; for (var i = 0; i < ` + itoa(n) + `; i++) a.push(i); (function() { var c = 0; for (var v of a) c++; return c; })`)
		if err != nil {
			t.Fatal(err)
		}
		f, _ := AssertFunction(v)
		if _, err := f(nil); err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(50, func() {
			if _, err := f(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	small, large := measure(10), measure(1000)
	if large != small {
		t.Fatalf("for-of allocations scale with length: %v allocs for 10 elements, %v for 1000", small, large)
	}
	if small > 8 {
		t.Fatalf("for-of over 10 elements allocated %v times, want <= 8", small)
	}

	// Runtime.ForOf (Go side) goes through the same record.
	r := New()
	arr, err := r.RunString(`var b = []; for (var i = 0; i < 1000; i++) b.push(i); b`)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	allocs := testing.AllocsPerRun(50, func() {
		sum = 0
		r.ForOf(arr, func(v Value) bool { sum += v.ToInteger(); return true })
	})
	if sum != 499500 {
		t.Fatalf("ForOf sum = %d", sum)
	}
	if allocs > 8 {
		t.Fatalf("ForOf over 1000 elements allocated %v times, want <= 8", allocs)
	}
}

func itoa(n int) string {
	return intToValue(int64(n)).String()
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `cd /home/lk/Work/goja && go test -run 'TestIteratorFastPath' . 2>&1 | tail -5`
Expected: compile error `r.getIterator(v, nil).fast undefined (type *iteratorRecord has no field or method fast)`.

- [ ] **Step 5: Add the anchor fields and the record/gate in `runtime.go`**

In the `global` struct, directly after the line `RegExpStringIteratorPrototype *Object`, add:

```go
	// Intrinsic iterator next functions. iteratorRecord takes its
	// allocation-free path only when the resolved next is one of these.
	arrayIterProtoNext        *Object
	regExpStringIterProtoNext *Object
```

Replace the `iteratorRecord` type and `getIterator` with:

```go
// iterNexter is implemented by intrinsic iterator objects whose next() can
// be evaluated in Go without materializing the {value, done} result object.
// nextValue must perform exactly the observable work of next(): the same
// reads, in the same order, with the same state transitions. When done is
// false, value must be non-nil.
type iterNexter interface {
	nextValue() (value Value, done bool)
}

type iteratorRecord struct {
	iterator *Object
	next     func(FunctionCall) Value
	// fast is non-nil only when getIterator verified that next is this
	// runtime's own intrinsic next for the iterator's concrete type, so
	// calling nextValue is indistinguishable from calling next.
	fast iterNexter
}

// intrinsicIterNexter returns iter's Go-level nexter when nextObj is the
// runtime's own intrinsic next function for iter's concrete type. Any
// tampering — a replaced Symbol.iterator, a replaced or shadowed next, an
// accessor, a user iterator, or an object from another Runtime — fails one
// of the two checks and yields nil, so the generic protocol is used.
func (r *Runtime) intrinsicIterNexter(iter, nextObj *Object) iterNexter {
	switch it := iter.self.(type) {
	case *arrayIterObject:
		if nextObj == r.global.arrayIterProtoNext {
			return it
		}
	}
	return nil
}

func (r *Runtime) getIterator(obj Value, method func(FunctionCall) Value) *iteratorRecord {
	if method == nil {
		method = toMethod(r.getV(obj, SymIterator))
		if method == nil {
			panic(r.NewTypeError("object is not iterable"))
		}
	}

	iter := r.toObject(method(FunctionCall{
		This: obj,
	}))

	var next func(FunctionCall) Value
	var fast iterNexter

	if obj, ok := iter.self.getStr("next", nil).(*Object); ok {
		if call, ok := obj.self.assertCallable(); ok {
			next = call
			fast = r.intrinsicIterNexter(iter, obj)
		}
	}

	return &iteratorRecord{
		iterator: iter,
		next:     next,
		fast:     fast,
	}
}
```

Replace `iterate`, `step`, `returnIter` and `close` with:

```go
func (ir *iteratorRecord) iterate(step func(Value)) {
	r := ir.iterator.runtime
	for {
		if ir.next == nil {
			panic(r.NewTypeError("iterator.next is missing or not a function"))
		}
		var value Value
		if ir.fast != nil {
			var done bool
			value, done = ir.fast.nextValue()
			if done {
				break
			}
		} else {
			res := r.toObject(ir.next(FunctionCall{This: ir.iterator}))
			if iteratorComplete(res) {
				break
			}
			value = iteratorValue(res)
		}
		ret := tryFunc(func() {
			step(value)
		})
		if ret != nil {
			_ = tryFunc(func() {
				ir.returnIter()
			})
			panic(ret)
		}
	}
}

func (ir *iteratorRecord) step() (value Value, ex *Exception) {
	r := ir.iterator.runtime
	ex = r.vm.try(func() {
		var done bool
		if ir.fast != nil {
			var v Value
			v, done = ir.fast.nextValue()
			if !done {
				value = v
			}
		} else {
			res := r.toObject(ir.next(FunctionCall{This: ir.iterator}))
			done = iteratorComplete(res)
			if !done {
				value = iteratorValue(res)
			}
		}
		if done {
			ir.close()
		}
	})
	return
}

func (ir *iteratorRecord) returnIter() {
	if ir.iterator == nil {
		return
	}
	retMethod := toMethod(ir.iterator.self.getStr("return", nil))
	if retMethod != nil {
		ir.iterator.runtime.toObject(retMethod(FunctionCall{This: ir.iterator}))
	}
	ir.iterator = nil
	ir.next = nil
	ir.fast = nil
}

func (ir *iteratorRecord) close() {
	ir.iterator = nil
	ir.next = nil
	ir.fast = nil
}
```

- [ ] **Step 6: Store the anchor in `builtin_array.go`**

Change `createArrayIterProto` to:

```go
func (r *Runtime) createArrayIterProto(val *Object) objectImpl {
	o := newBaseObjectObj(val, r.getIteratorPrototype(), classObject)

	next := r.newNativeFunc(r.arrayIterProto_next, "next", 0)
	r.global.arrayIterProtoNext = next
	o._putProp("next", next, true, false, true)
	o._putSym(SymToStringTag, valueProp(asciiString(classArrayIterator), false, false, true))

	return o
}
```

- [ ] **Step 7: Split `arrayIterObject.next` in `array.go`**

Replace the existing `func (ai *arrayIterObject) next() Value` with:

```go
// nextValue is the body of %ArrayIteratorPrototype%.next without the result
// object: same reads (length, element), same order, same state transitions.
// iteratorRecord calls it directly when the intrinsic next is in effect.
func (ai *arrayIterObject) nextValue() (Value, bool) {
	if ai.obj == nil {
		return _undefined, true
	}
	if ta, ok := ai.obj.self.(*typedArrayObject); ok {
		ta.viewedArrayBuf.ensureNotDetached(true)
	}
	l := toLength(ai.obj.self.getStr("length", nil))
	index := ai.nextIdx
	if index >= l {
		ai.obj = nil
		return _undefined, true
	}
	ai.nextIdx++
	idxVal := valueInt(index)
	if ai.kind == iterationKindKey {
		return idxVal, false
	}
	elementValue := nilSafe(ai.obj.self.getIdx(idxVal, nil))
	if ai.kind == iterationKindValue {
		return elementValue, false
	}
	return ai.val.runtime.newArrayValues([]Value{idxVal, elementValue}), false
}

func (ai *arrayIterObject) next() Value {
	v, done := ai.nextValue()
	return ai.val.runtime.createIterResultObject(v, done)
}
```

- [ ] **Step 8: Run the tests to verify they pass, including under the race detector**

Run:
```bash
cd /home/lk/Work/goja && go test -run 'TestIteratorFastPath' -v . 2>&1 | grep -E '^(--- |ok|FAIL|panic)' && go test -race -run 'TestIteratorFastPathGate|TestIteratorFastPathObservable' . 2>&1 | tail -2
```
Expected: three `--- PASS` lines; race run `ok`. (`TestIteratorFastPathAllocationFree` is excluded from the race run because instrumentation perturbs allocation counts.)

- [ ] **Step 9: Measure against the baseline**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkForOfArray$|BenchmarkArraySpread$|BenchmarkArrayDestructure$|BenchmarkArrayFrom$' -benchmem -count=10 . > /tmp/goja-bench/b2-task1.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/b2-task1-base.txt /tmp/goja-bench/b2-task1.txt
```
Acceptance (deterministic): `ForOfArray` and `ArrayDestructure` allocs/op ≤ 8; `ArraySpread` and `ArrayFrom` allocs/op ≤ 25 (the result array's growth remains); every row's allocs/op reduced by ≥ 98% versus the baseline file. Time deltas are directional. If any rule fails, STOP and report with the table — do not tune.

- [ ] **Step 10: Full suite and static checks**

Run (timeout 600000 ms):
```bash
cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6
```
Expected: clean gofmt/vet; all `ok` including test262.

- [ ] **Step 11: Commit**

```bash
cd /home/lk/Work/goja && git add runtime.go builtin_array.go array.go iterator_fastpath_test.go && git commit -F - <<'MSG'
Skip iterator result objects for intrinsic array iterators

getIterator now records a Go-level nextValue() on the iteratorRecord when
the iterator is an *arrayIterObject and the resolved next is this
runtime's own %ArrayIteratorPrototype%.next object. step() and iterate(),
the only consumers, call it instead of materializing {value, done}, so
for-of, spread, destructuring, Array.from, Map/Set construction and
Runtime.ForOf over arrays no longer allocate per element. Any tampering
(replaced Symbol.iterator, replaced or shadowed next, accessor, user
iterator, cross-runtime object) fails the identity check and takes the
unchanged generic path. next() is now nextValue() plus the result object,
so the observable reads are the same code on both paths.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 2: Fast path for the regexp-string iterator (spec §4)

**Files:**
- Modify: `builtin_regexp.go` — `regExpStringIterObject.next` (~line 866) and `createRegExpStringIteratorPrototype` (~line 1297)
- Modify: `runtime.go` — `intrinsicIterNexter` (add one case)
- Test: `iterator_fastpath_test.go` (append)

**Interfaces:**
- Consumes: `iterNexter`, `intrinsicIterNexter`, `r.global.regExpStringIterProtoNext` (declared in Task 1), `fastPathEngaged`, `runBench`.
- Produces: `func (ri *regExpStringIterObject) nextValue() (Value, bool)`.

- [ ] **Step 1: Add the benchmark and capture its baseline**

Append to `iterator_fastpath_test.go`:

```go
func BenchmarkMatchAllLoop(b *testing.B) {
	runBench(b, `var s = "ab".repeat(500); var re = /a/g; (function() { var n = 0; for (var m of s.matchAll(re)) n += m[0].length; return n; })`)
}
```

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkMatchAllLoop$' -benchmem -count=10 . > /tmp/goja-bench/b2-task2-base.txt 2>&1 && grep -E "^Benchmark" /tmp/goja-bench/b2-task2-base.txt | head -2
```
Expected: 10 lines; record the baseline allocs/op (several thousand: 501 result objects plus per-match arrays plus one regex compile per call — the compile is Task 4's concern).

- [ ] **Step 2: Write the failing tests**

Append to `iterator_fastpath_test.go`:

```go
func TestRegExpStringIteratorFastPath(t *testing.T) {
	if !fastPathEngaged(t, New(), `"abc".matchAll(/b/g)`) {
		t.Fatal("expected fast path for a RegExp String Iterator")
	}
	r := New()
	if _, err := r.RunString(`var __rp = Object.getPrototypeOf("a".matchAll(/a/g)); var __rn = __rp.next; __rp.next = function() { return __rn.call(this); };`); err != nil {
		t.Fatal(err)
	}
	if fastPathEngaged(t, r, `"abc".matchAll(/b/g)`) {
		t.Fatal("replaced %RegExpStringIteratorPrototype%.next must disable the fast path")
	}
	const wrapNext = `var __rp = Object.getPrototypeOf("a".matchAll(/a/g)); var __rn = __rp.next; __rp.next = function() { return __rn.call(this); };`
	cases := []struct{ name, script, want string }{
		{"global", `[..."a1b2c3".matchAll(/\d/g)].map(function(m) { return m[0]; }).join("");`, "123"},
		{"empty matches advance", `[..."ab".matchAll(/(?:)/g)].map(function(m) { return m.index; }).join(",");`, "0,1,2"},
		{"sticky+global", `[..."aab".matchAll(/a/gy)].map(function(m) { return m.index; }).join(",");`, "0,1"},
		{"groups preserved", `var out = []; for (var m of "k=v;x=y".matchAll(/(\w)=(\w)/g)) out.push(m[1] + m[2]); out.join("|");`, "kv|xy"},
		{"direct next()", `var it = "aa".matchAll(/a/g); var r1 = it.next(); var r2 = it.next(); var r3 = it.next(); [r1.value[0], r1.done, r2.done, r3.done, r3.value].join(",");`, "a,false,false,true,"},
		{"incompatible receiver", `var msg = ""; try { Object.getPrototypeOf("a".matchAll(/a/g)).next.call([][Symbol.iterator]()); } catch (e) { msg = e.constructor.name; } msg;`, "TypeError"},
	}
	for _, c := range cases {
		for _, generic := range []bool{false, true} {
			r := New()
			src := c.script
			if generic {
				src = wrapNext + src
			}
			v, err := r.RunString(src)
			if err != nil {
				t.Fatalf("%s (generic=%v): %v", c.name, generic, err)
			}
			if got := v.String(); got != c.want {
				t.Fatalf("%s (generic=%v): got %q, want %q", c.name, generic, got, c.want)
			}
		}
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd /home/lk/Work/goja && go test -run 'TestRegExpStringIteratorFastPath' . 2>&1 | tail -4`
Expected: FAIL with `expected fast path for a RegExp String Iterator`.

- [ ] **Step 4: Split `next`, store the anchor, extend the gate**

In `builtin_regexp.go`, replace `func (ri *regExpStringIterObject) next() (v Value)` with:

```go
// nextValue is the body of %RegExpStringIteratorPrototype%.next without the
// result object: same regExpExec call, same done latch, same lastIndex
// advance on empty matches.
func (ri *regExpStringIterObject) nextValue() (Value, bool) {
	if ri.done {
		return _undefined, true
	}

	match := regExpExec(ri.matcher, ri.s)
	if IsNull(match) {
		ri.done = true
		return _undefined, true
	}
	if !ri.global {
		ri.done = true
		return match, false
	}

	matchStr := nilSafe(ri.val.runtime.toObject(match).self.getIdx(valueInt(0), nil)).toString()
	if matchStr.Length() == 0 {
		thisIndex := toLength(ri.matcher.self.getStr("lastIndex", nil))
		ri.matcher.self.setOwnStr("lastIndex", valueInt(advanceStringIndex64(ri.s, thisIndex, ri.fullUnicode)), true)
	}
	return match, false
}

func (ri *regExpStringIterObject) next() Value {
	v, done := ri.nextValue()
	return ri.val.runtime.createIterResultObject(v, done)
}
```

Change `createRegExpStringIteratorPrototype` to:

```go
func (r *Runtime) createRegExpStringIteratorPrototype(val *Object) objectImpl {
	o := newBaseObjectObj(val, r.getIteratorPrototype(), classObject)

	next := r.newNativeFunc(r.regExpStringIteratorProto_next, "next", 0)
	r.global.regExpStringIterProtoNext = next
	o._putProp("next", next, true, false, true)
	o._putSym(SymToStringTag, valueProp(asciiString(classRegExpStringIterator), false, false, true))

	return o
}
```

In `runtime.go`, extend `intrinsicIterNexter`'s switch with:

```go
	case *regExpStringIterObject:
		if nextObj == r.global.regExpStringIterProtoNext {
			return it
		}
```

- [ ] **Step 5: Run the tests to verify they pass, including under race**

Run:
```bash
cd /home/lk/Work/goja && go test -run 'TestRegExpStringIteratorFastPath|TestIteratorFastPath' -v . 2>&1 | grep -E '^(--- |ok|FAIL|panic)' && go test -race -run 'TestRegExpStringIteratorFastPath' . 2>&1 | tail -2
```
Expected: all `--- PASS`; race `ok`.

- [ ] **Step 6: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkMatchAllLoop$' -benchmem -count=10 . > /tmp/goja-bench/b2-task2.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/b2-task2-base.txt /tmp/goja-bench/b2-task2.txt
```
Acceptance: allocs/op decreases by at least 1000 versus the baseline (501 result objects, each at least an object plus a map). If it does not, STOP and report.

- [ ] **Step 7: Full suite and static checks**

Run (timeout 600000 ms):
```bash
cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6
```
Expected: clean; all `ok` including test262.

- [ ] **Step 8: Commit**

```bash
cd /home/lk/Work/goja && git add builtin_regexp.go runtime.go iterator_fastpath_test.go && git commit -F - <<'MSG'
Extend the intrinsic iterator fast path to RegExp String Iterators

String.prototype.matchAll loops no longer allocate a {value, done} object
per match when %RegExpStringIteratorPrototype%.next is untouched. The
match arrays themselves are observable and remain.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 3: `GetOwnDataValue` (spec §5)

**Files:**
- Modify: `value.go` — add after `func (o *Object) GetSymbol` (~line 858)
- Test: `object_test.go` (append)

**Interfaces:**
- Produces: `func (o *Object) GetOwnDataValue(name string) (v Value, ok bool)` — public API.

- [ ] **Step 1: Add the benchmarks and capture the baseline of the descriptor path**

Append to `object_test.go`:

```go
func BenchmarkGetOwnDataValue(b *testing.B) {
	r := New()
	v, err := r.RunString(`({a: 1, b: "two"})`)
	if err != nil {
		b.Fatal(err)
	}
	o := v.ToObject(r)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := o.GetOwnDataValue("a"); !ok {
			b.Fatal("missing")
		}
	}
}

// BenchmarkGetOwnPropertyDescriptorValue is the path GetOwnDataValue replaces:
// calling Object.getOwnPropertyDescriptor from Go and reading .value.
func BenchmarkGetOwnPropertyDescriptorValue(b *testing.B) {
	r := New()
	v, err := r.RunString(`({a: 1, b: "two"})`)
	if err != nil {
		b.Fatal(err)
	}
	o := v.ToObject(r)
	desc, ok := AssertFunction(r.Get("Object").ToObject(r).Get("getOwnPropertyDescriptor"))
	if !ok {
		b.Fatal("no getOwnPropertyDescriptor")
	}
	key := r.ToValue("a")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, err := desc(Undefined(), o, key)
		if err != nil {
			b.Fatal(err)
		}
		if d.ToObject(r).Get("value") == nil {
			b.Fatal("missing")
		}
	}
}
```

`BenchmarkGetOwnDataValue` will not compile until Step 4; capture the descriptor baseline now:

```bash
cd /home/lk/Work/goja && sed -i 's/if _, ok := o.GetOwnDataValue("a"); !ok {/if _, ok := o.Get("a").(Value); !ok { \/\/ BASELINE-STUB/' object_test.go && \
go test -run=NONE -bench='BenchmarkGetOwnPropertyDescriptorValue$' -benchmem -count=10 . > /tmp/goja-bench/b2-task3-base.txt 2>&1 ; \
sed -i 's/if _, ok := o.Get("a").(Value); !ok { \/\/ BASELINE-STUB/if _, ok := o.GetOwnDataValue("a"); !ok {/' object_test.go && grep -c BASELINE-STUB object_test.go; grep -E "^Benchmark" /tmp/goja-bench/b2-task3-base.txt | head -1
```
Expected: `0` (stub removed); the descriptor path shows several allocs/op.

- [ ] **Step 2: Write the failing test**

Append to `object_test.go`:

```go
func TestGetOwnDataValue(t *testing.T) {
	r := New()
	v, err := r.RunString(`
	var o = { a: 1, u: undefined, get g() { return 2; } };
	Object.defineProperty(o, "h", { value: 3, enumerable: false });
	var arr = [10, 20];
	var trapLog = [];
	var p = new Proxy({ x: 5 }, { getOwnPropertyDescriptor(t, k) { trapLog.push(String(k)); return Reflect.getOwnPropertyDescriptor(t, k); } });
	({ o: o, arr: arr, p: p, trapLog: trapLog });
	`)
	if err != nil {
		t.Fatal(err)
	}
	root := v.ToObject(r)
	o := root.Get("o").ToObject(r)
	arr := root.Get("arr").ToObject(r)
	p := root.Get("p").ToObject(r)

	check := func(obj *Object, name string, wantOK bool, want Value) {
		t.Helper()
		got, ok := obj.GetOwnDataValue(name)
		if ok != wantOK {
			t.Fatalf("%s: ok = %v, want %v", name, ok, wantOK)
		}
		if !wantOK {
			if got != nil {
				t.Fatalf("%s: value must be nil when ok is false, got %v", name, got)
			}
			return
		}
		if !got.StrictEquals(want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
	}
	check(o, "a", true, intToValue(1))
	check(o, "u", true, _undefined)
	check(o, "g", false, nil)
	check(o, "h", true, intToValue(3))
	check(o, "missing", false, nil)
	check(o, "toString", false, nil) // inherited, not own
	check(arr, "0", true, intToValue(10))
	check(arr, "1", true, intToValue(20))
	check(arr, "2", false, nil)
	check(arr, "length", true, intToValue(2))
	check(p, "x", true, intToValue(5))
	check(p, "y", false, nil)
	if log := root.Get("trapLog").ToObject(r); log.Get("length").ToInteger() != 2 || log.Get("0").String() != "x" || log.Get("1").String() != "y" {
		t.Fatalf("proxy trap must run for each lookup, got %v", log.Export())
	}

	goObj := r.ToValue(struct{ A int }{7}).ToObject(r)
	check(goObj, "A", true, intToValue(7))
	check(goObj, "B", false, nil)

	if allocs := testing.AllocsPerRun(1000, func() { o.GetOwnDataValue("a") }); allocs != 0 {
		t.Fatalf("GetOwnDataValue allocated %v times per call, want 0", allocs)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd /home/lk/Work/goja && go test -run 'TestGetOwnDataValue' . 2>&1 | tail -4`
Expected: compile error `o.GetOwnDataValue undefined`.

- [ ] **Step 4: Implement**

In `value.go`, after `func (o *Object) GetSymbol(sym *Symbol) Value { ... }`, add:

```go
// GetOwnDataValue returns the value of the object's own data property name
// without invoking accessors and without allocating a property descriptor.
// ok is false when the property is absent or is an accessor property, so
// (Undefined, true) — an own data property holding undefined — is
// distinguishable from (nil, false). Exotic objects behave as for
// Object.getOwnPropertyDescriptor: a Proxy's getOwnPropertyDescriptor trap
// still runs.
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) GetOwnDataValue(name string) (v Value, ok bool) {
	switch p := o.self.getOwnPropStr(unistring.NewFromString(name)).(type) {
	case nil:
		return nil, false
	case *valueProperty:
		if p.accessor {
			return nil, false
		}
		return p.value, true
	default:
		return p, true
	}
}
```

(`getOwnPropStr` is the same resolver `Object.getOwnPropertyDescriptor` reaches for string keys; array implementations parse index-shaped names inside it, so `"0"` on an array resolves to the element.)

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd /home/lk/Work/goja && go test -run 'TestGetOwnDataValue' -v . 2>&1 | tail -3 && go test -race -run 'TestGetOwnDataValue' . 2>&1 | tail -1`
Expected: `--- PASS`; race `ok` (the allocation assertion may be reported non-zero under race instrumentation only if it fails — the non-race run is authoritative; if the race run fails solely on the allocation line, run it once more with `-skip` absent and report both).

- [ ] **Step 6: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkGetOwnDataValue$|BenchmarkGetOwnPropertyDescriptorValue$' -benchmem -count=10 . 2>&1 | grep -E "^Benchmark" | sort | awk '{a[$1]=$0} END{for(k in a) print a[k]}'
```
Acceptance: `BenchmarkGetOwnDataValue` reports `0 allocs/op`; `BenchmarkGetOwnPropertyDescriptorValue` is unchanged from the Step 1 baseline (it is the comparison, not a target). Paste both rows into the commit message.

- [ ] **Step 7: Full suite and static checks**

Run (timeout 600000 ms):
```bash
cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6
```
Expected: clean; all `ok`.

- [ ] **Step 8: Commit**

```bash
cd /home/lk/Work/goja && git add value.go object_test.go && git commit -F - <<'MSG'
Add Object.GetOwnDataValue for descriptor-free own property reads

Host code that needs an own data property's value without invoking
accessors previously had to call Object.getOwnPropertyDescriptor from Go
and read .value, allocating a descriptor object per lookup.
GetOwnDataValue resolves through the same own-property path (Proxy traps
still run) and allocates nothing.

<paste the two benchmark rows here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 4: Bounded global compiled-regex cache (spec §6)

**Files:**
- Modify: `builtin_regexp.go` — `compileRegexp` (~line 188) and the import block
- Test: `regexp_test.go` (append)

**Interfaces:**
- Produces: `compileRegexpUncached(patternStr, flags string) (*regexpPattern, error)` (the renamed existing function), package vars `regexpCache` and const `regexpCacheMaxEntries = 1024`; `compileRegexp` keeps its signature.

- [ ] **Step 1: Add the benchmark and capture the baseline**

Append to `regexp_test.go`:

```go
func BenchmarkRegExpCtorRepeated(b *testing.B) {
	r := New()
	v, err := r.RunString(`(function() { var n = 0; for (var i = 0; i < 100; i++) { if (new RegExp("a+b", "g").test("aab")) n++; } return n; })`)
	if err != nil {
		b.Fatal(err)
	}
	f, _ := AssertFunction(v)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f(nil); err != nil {
			b.Fatal(err)
		}
	}
}
```

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkRegExpCtorRepeated$|BenchmarkMatchAllLoop$' -benchmem -count=10 . > /tmp/goja-bench/b2-task4-base.txt 2>&1 && grep -E "^Benchmark" /tmp/goja-bench/b2-task4-base.txt | sort -u -k1,1 | head -2
```
Expected: two benchmark names present; record allocs/op for both (`RegExpCtorRepeated` compiles 100 patterns per op; `MatchAllLoop` compiles one species clone per op).

- [ ] **Step 2: Write the failing tests**

Append to `regexp_test.go`:

```go
func resetRegexpCache() {
	regexpCache.Lock()
	regexpCache.m = make(map[regexpCacheKey]*regexpPattern)
	regexpCache.Unlock()
}

func regexpCacheLen() int {
	regexpCache.Lock()
	defer regexpCache.Unlock()
	return len(regexpCache.m)
}

func TestRegexpCacheReturnsIndependentClones(t *testing.T) {
	resetRegexpCache()
	p1, err := compileRegexp("a(b)", "g")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := compileRegexp("a(b)", "g")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatal("cache must hand out distinct clones, not the template")
	}
	if regexpCacheLen() != 1 {
		t.Fatalf("cache len = %d, want 1", regexpCacheLen())
	}
	// A pattern that requires regexp2 (lookbehind) must share the compiled
	// engine but not the per-clone match cache.
	q1, err := compileRegexp("(?<=a)b", "")
	if err != nil {
		t.Fatal(err)
	}
	q2, err := compileRegexp("(?<=a)b", "")
	if err != nil {
		t.Fatal(err)
	}
	if q1.regexp2Wrapper == nil || q2.regexp2Wrapper == nil {
		t.Fatal("expected regexp2 wrappers")
	}
	if q1.regexp2Wrapper == q2.regexp2Wrapper {
		t.Fatal("regexp2 wrapper must be cloned per caller")
	}
	if q1.regexp2Wrapper.rx != q2.regexp2Wrapper.rx {
		t.Fatal("compiled regexp2 engine must be shared")
	}
	if q1.regexp2Wrapper.cache != nil {
		t.Fatal("clone must start with an empty match cache")
	}
}

func TestRegexpCacheRuntimeStateIndependent(t *testing.T) {
	resetRegexpCache()
	const mk = `var re = new RegExp("a", "g");`
	r1, r2 := New(), New()
	v, err := r1.RunString(mk + `re.test("aa"); re.lastIndex`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 1 {
		t.Fatalf("r1 lastIndex = %v, want 1", v)
	}
	v, err = r2.RunString(mk + `re.lastIndex`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 0 {
		t.Fatalf("r2 lastIndex = %v, want 0 (state leaked across runtimes)", v)
	}
	v, err = r1.RunString(`re.test("aa"); re.lastIndex`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 2 {
		t.Fatalf("r1 second lastIndex = %v, want 2", v)
	}
	// matchAll's per-call species clone is the main runtime compile site.
	v, err = r1.RunString(`var out = []; for (var i = 0; i < 3; i++) { out.push([..."aXa".matchAll(/a/g)].length); } out.join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "2,2,2" {
		t.Fatalf("matchAll results = %v", v)
	}
}

func TestRegexpCacheCapResets(t *testing.T) {
	resetRegexpCache()
	for i := 0; i < regexpCacheMaxEntries+5; i++ {
		if _, err := compileRegexp("p"+intToValue(int64(i)).String(), ""); err != nil {
			t.Fatal(err)
		}
	}
	if n := regexpCacheLen(); n == 0 || n > regexpCacheMaxEntries {
		t.Fatalf("cache len after overflow = %d, want in (0, %d]", n, regexpCacheMaxEntries)
	}
	if _, err := compileRegexp("after-reset", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := compileRegexp("after-reset", ""); err != nil {
		t.Fatal(err)
	}
}

func TestRegexpCacheInvalidNotCached(t *testing.T) {
	resetRegexpCache()
	if _, err := compileRegexp("(", ""); err == nil {
		t.Fatal("expected an error for an unterminated group")
	}
	if _, err := compileRegexp("(", ""); err == nil {
		t.Fatal("error must be reported again, not served from cache")
	}
	if regexpCacheLen() != 0 {
		t.Fatalf("invalid pattern was cached")
	}
	if _, err := compileRegexp("x", "gg"); err == nil {
		t.Fatal("expected an error for duplicate flags")
	}
	if regexpCacheLen() != 0 {
		t.Fatalf("invalid flags were cached")
	}
}

func TestRegexpCacheConcurrent(t *testing.T) {
	resetRegexpCache()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := New()
			for i := 0; i < 100; i++ {
				v, err := r.RunString(`new RegExp("x+y", "i").test("XXY") && new RegExp("(?<=a)b").test("ab")`)
				if err != nil {
					errs <- err
					return
				}
				if !v.ToBoolean() {
					errs <- fmt.Errorf("unexpected false")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestRegexpCacheHitAllocations(t *testing.T) {
	resetRegexpCache()
	if _, err := compileRegexp("warm", "g"); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := compileRegexp("warm", "g"); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 2 {
		t.Fatalf("cache hit allocated %v times, want <= 2 (the clone)", allocs)
	}
}
```

Ensure `regexp_test.go` imports `"fmt"`, `"sync"` and `"testing"` (add any that are missing).

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd /home/lk/Work/goja && go test -run 'TestRegexpCache' . 2>&1 | tail -4`
Expected: compile error `undefined: regexpCache`.

- [ ] **Step 4: Implement the cache**

In `builtin_regexp.go`: rename the existing `func compileRegexp(patternStr, flags string) (p *regexpPattern, err error)` to `func compileRegexpUncached(patternStr, flags string) (p *regexpPattern, err error)` (body unchanged), add `"sync"` to the import block, and add directly above it:

```go
type regexpCacheKey struct {
	src, flags string
}

// regexpCache holds immutable compiled pattern templates shared by every
// Runtime in the process. compileRegexp hands out clone()s, never the
// template itself — exactly what the compiler does for regexp literals —
// and the underlying *regexp.Regexp and *regexp2.Regexp are safe for
// concurrent use. Per-object state (lastIndex, the regexp2 match cache)
// lives on the clone or the regexpObject.
var regexpCache = struct {
	sync.Mutex
	m map[regexpCacheKey]*regexpPattern
}{m: make(map[regexpCacheKey]*regexpPattern)}

// regexpCacheMaxEntries bounds the cache. Patterns built from user data are
// unbounded; when the cap is reached the whole map is dropped, which keeps
// memory at O(cap) with no LRU bookkeeping.
const regexpCacheMaxEntries = 1024

func compileRegexp(patternStr, flags string) (*regexpPattern, error) {
	key := regexpCacheKey{src: patternStr, flags: flags}
	regexpCache.Lock()
	template := regexpCache.m[key]
	regexpCache.Unlock()
	if template != nil {
		return template.clone(), nil
	}
	template, err := compileRegexpUncached(patternStr, flags)
	if err != nil {
		return nil, err
	}
	regexpCache.Lock()
	if len(regexpCache.m) >= regexpCacheMaxEntries {
		regexpCache.m = make(map[regexpCacheKey]*regexpPattern)
	}
	regexpCache.m[key] = template
	regexpCache.Unlock()
	return template.clone(), nil
}
```

- [ ] **Step 5: Run the tests to verify they pass, including under race**

Run:
```bash
cd /home/lk/Work/goja && go test -run 'TestRegexpCache' -v . 2>&1 | grep -E '^(--- |ok|FAIL|panic)' && go test -race -run 'TestRegexpCacheConcurrent|TestRegexpCacheRuntimeStateIndependent|TestRegexpCacheReturnsIndependentClones' . 2>&1 | tail -2
```
Expected: six `--- PASS`; race `ok`.

- [ ] **Step 6: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkRegExpCtorRepeated$|BenchmarkMatchAllLoop$' -benchmem -count=10 . > /tmp/goja-bench/b2-task4.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/b2-task4-base.txt /tmp/goja-bench/b2-task4.txt
```
Acceptance: `RegExpCtorRepeated` allocs/op decreases by at least 30% versus baseline (100 compilations per op removed); `MatchAllLoop` allocs/op decreases (one compilation per op removed) and does not increase. If either fails, STOP and report.

- [ ] **Step 7: Full suite and static checks**

Run (timeout 600000 ms):
```bash
cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6
```
Expected: clean; all `ok` including test262 (many test262 regexp tests exercise the cache).

- [ ] **Step 8: Commit**

```bash
cd /home/lk/Work/goja && git add builtin_regexp.go regexp_test.go && git commit -F - <<'MSG'
Cache compiled regex patterns across runtimes

compileRegexp now keeps a bounded process-global map of immutable
compiled templates keyed by (source, flags) and hands out clone()s, the
same sharing the compiler already relies on for regexp literals. The
runtime path — new RegExp(), RegExp(), and String.prototype.matchAll's
per-call species clone — no longer recompiles on every call. The cache
is capped at 1024 entries and dropped wholesale when full; errors are
not cached.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 5: Final verification and hand-off

**Files:**
- Modify: `docs/superpowers/specs/2026-09-16-iterator-fast-path-batch2-design.md` (status line and one §5 sentence)

**Interfaces:** none.

- [ ] **Step 1: Clean-tree verification**

Run (timeout 600000 ms):
```bash
cd /home/lk/Work/goja && git status --porcelain && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | grep -v "no test files" | tail -6 && go test -race -count=1 -run 'TestIteratorFastPathGate|TestIteratorFastPathObservable|TestRegExpStringIteratorFastPath|TestGetOwnDataValue|TestRegexpCache' . 2>&1 | tail -2
```
Expected: `git status` prints nothing; gofmt/vet clean; all `ok`; race run `ok`.

- [ ] **Step 2: Whole-branch benchmark summary**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkForOfArray$|BenchmarkArraySpread$|BenchmarkArrayDestructure$|BenchmarkArrayFrom$|BenchmarkMatchAllLoop$|BenchmarkRegExpCtorRepeated$|BenchmarkGetOwnDataValue$|BenchmarkGetOwnPropertyDescriptorValue$|BenchmarkCallJS$|BenchmarkFib$|BenchmarkVMAdd$' -benchmem -count=10 . 2>&1 | grep -E "^Benchmark" | sort -k1,1 | awk '{k=$1; a[k]=$0} END{for (k in a) print a[k]}' | sort | tee /tmp/goja-bench/b2-summary.txt
```
Expected: one row per benchmark; the four array benchmarks and `GetOwnDataValue` at their Task acceptance values; `CallJS`/`Fib`/`VMAdd` (unrelated) present as regression sentinels — compare them against `/tmp/goja-bench/final.txt` from batch 1 with `~/go/bin/benchstat` if that file still exists; any significant regression blocks the hand-off.

- [ ] **Step 3: Update the spec and commit**

In the spec: change `**Status:**` to `implemented on branch perf/iterator-fast-path-batch2 (2026-09-16); per-change benchmarks in commit messages`, and in §5 replace the sentence beginning `Implementation: \`p := o.getOwnProp(newStringValue(name))\`` so it reads `Implementation: \`o.self.getOwnPropStr(unistring.NewFromString(name))\` (the same string-key resolver \`Get\` uses; array implementations parse index-shaped names inside it, so \`"0"\` resolves to the element and Proxy traps still run), then:`. Then:

```bash
cd /home/lk/Work/goja && git add docs/superpowers/specs/2026-09-16-iterator-fast-path-batch2-design.md && git commit -q -m "Mark iterator fast path batch 2 spec as implemented

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>" && git log --oneline master..HEAD
```
Expected: six commits on the branch (spec, plan, four changes, status update) — seven if the plan was committed separately from the spec.

- [ ] **Step 4: Report**

Report the commit list, the head SHA, and `/tmp/goja-bench/b2-summary.txt`. Do **not** push and do **not** merge: the branch is pushed to the fork by the controller after the user confirms, and merged to `master` only after the user's GAP qualification.
