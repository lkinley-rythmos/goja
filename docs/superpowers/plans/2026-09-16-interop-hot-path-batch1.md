# Embedded-Interop Hot Path Batch 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut per-call cost and allocation volume for goja embedded in Go services by landing four independent, individually measured runtime changes.

**Architecture:** Four surgical edits to the existing interpreter: keep the VM value stack's backing array alive between top-level calls (`runtime.go`), emit a dedicated `getLength` instruction for `x.length` reads that short-circuits string primitives (`compiler_expr.go`, `vm.go`), precompute reflect metadata once per wrapped Go function (`runtime.go`), and replace the 256-entry boxed-int cache with a pointer-free 66 560-entry table (`value.go`, `vm.go`). No public API changes. Each task is one commit with its own tests and benchmark delta.

**Tech Stack:** Go 1.25 (module floor), `testing` benchmarks, `benchstat` (`~/go/bin/benchstat`), tc39 test262 via `.tc39_test262_checkout.sh`.

**Spec:** `docs/superpowers/specs/2026-09-16-interop-hot-path-batch1-design.md`

## Global Constraints

- Go floor is `go 1.25.0` (from `go.mod`); `clear()` and range-over-int are available.
- All work on branch `perf/interop-hot-path-batch1` (already created, spec committed at `d27f723`).
- Every commit must pass: `gofmt -l .` (empty output), `go vet ./...`, `go test ./...` **with test262 checked out** (Task 0), and — for Task 4 — `go test -race -run 'TestIntCache' .`.
- Tests and benchmarks go in goja's own `*_test.go` files (package `goja`, internal access allowed). Never commit anything under `testdata/test262`.
- No public API additions or changes. No new dependencies.
- Commit messages end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Benchmark acceptance is judged on `allocs/op` (deterministic, must match the numbers in each task exactly) and on `benchstat` time deltas over `-count=10` (directional; the prototyping machine throttles, so a task is not rejected for a noisy time delta if allocs match and nothing regresses beyond noise).

---

### Task 0: Prerequisites and baseline

**Files:**
- Create (untracked, git-ignored by existing rules): `testdata/test262/` via `.tc39_test262_checkout.sh`
- Create (outside repo): `/tmp/goja-bench/base.txt`

**Interfaces:**
- Produces: `/tmp/goja-bench/base.txt` — baseline benchmark output on `master` consumed by every later task's benchstat step.

- [ ] **Step 1: Check out test262 at the pinned commit**

Run:
```bash
cd /home/lk/Work/goja && ./.tc39_test262_checkout.sh
```
Expected: `testdata/test262` exists; `git status --porcelain` shows nothing (the directory is ignored).

- [ ] **Step 2: Confirm the full suite passes on the branch before any change**

Run:
```bash
cd /home/lk/Work/goja && go test -count=1 ./... 2>&1 | tail -8
```
Expected: every package `ok`; `TestTC39` runs (takes minutes) and is **not** reported as skipped. If it prints `If you want to run tc39 tests, download them ...`, Step 1 did not work — stop and fix.

- [ ] **Step 3: Record the baseline benchmarks from master**

Run:
```bash
cd /home/lk/Work/goja && mkdir -p /tmp/goja-bench && \
git checkout -q master && \
go test -run=NONE -bench='BenchmarkCallJS$|BenchmarkFuncCall$|BenchmarkCallReflect$|BenchmarkCallNative$|BenchmarkVMAdd$|BenchmarkFib$|BenchmarkGoReflectGet$' -benchmem -count=10 . > /tmp/goja-bench/base.txt 2>&1; \
git checkout -q perf/interop-hot-path-batch1 && tail -3 /tmp/goja-bench/base.txt
```
Expected: the last lines are `PASS` and `ok  github.com/dop251/goja  ...s`. The file contains 10 lines per benchmark.

Note: the benchmarks added in Tasks 1–4 do not exist on master; their baselines are captured inside each task by temporarily reverting the non-test change (each task says how).

---

### Task 1: Retain the VM value stack across top-level calls (spec §2)

**Files:**
- Modify: `runtime.go:2842-2852` (`func (r *Runtime) leave()`)
- Test: `runtime_test.go` (append at end of file)

**Interfaces:**
- Produces: package constant `maxRetainedStackCap = 1024` in `runtime.go`. Nothing else consumes it, but the test in this task references it by name.

- [ ] **Step 1: Write the failing tests**

Append to `runtime_test.go`:

```go
func TestCallableRetainsValueStack(t *testing.T) {
	r := New()
	if _, err := r.RunString(`function f() { return 1; }`); err != nil {
		t.Fatal(err)
	}
	f, ok := AssertFunction(r.Get("f"))
	if !ok {
		t.Fatal("f is not a function")
	}
	call := func() {
		if _, err := f(_undefined); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		call()
	}
	if cap(r.vm.stack) == 0 {
		t.Fatal("value stack was not retained after returning to Go")
	}
	if allocs := testing.AllocsPerRun(200, call); allocs != 0 {
		t.Fatalf("expected 0 allocs per Go->JS call, got %v", allocs)
	}
}

func TestLeaveDropsOversizedValueStack(t *testing.T) {
	r := New()
	const n = maxRetainedStackCap * 4
	_, err := r.RunString(`
		function f() { return arguments.length; }
		var big = new Array(` + strconv.Itoa(n) + `);
		f.apply(null, big);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if c := cap(r.vm.stack); c != 0 {
		t.Fatalf("expected oversized value stack to be dropped, cap=%d", c)
	}
	v, err := r.RunString(`f.apply(null, big)`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != n {
		t.Fatalf("unexpected result after stack drop: %v", v)
	}
	if c := cap(r.vm.stack); c != 0 {
		t.Fatalf("expected oversized value stack to be dropped again, cap=%d", c)
	}
}
```

Ensure `runtime_test.go` imports `"strconv"` (add it to the import block if absent).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd /home/lk/Work/goja && go test -run 'TestCallableRetainsValueStack|TestLeaveDropsOversizedValueStack' . 2>&1 | tail -5`
Expected: compile error `undefined: maxRetainedStackCap`.

- [ ] **Step 3: Implement the change**

In `runtime.go`, replace the body of `leave()`:

```go
// maxRetainedStackCap is the largest value-stack capacity (in slots) kept
// alive between top-level calls into the runtime. Below it the backing
// array is reused, which makes repeated Go->JS calls allocation-free; above
// it the array is released so a single deep call cannot pin memory for the
// lifetime of the Runtime.
const maxRetainedStackCap = 1024

// called when the top level function returns normally (i.e. control is passed outside the Runtime)
func (r *Runtime) leave() {
	var jobs []func()
	for len(r.jobQueue) > 0 {
		jobs, r.jobQueue = r.jobQueue, jobs[:0]
		for _, job := range jobs {
			job()
		}
	}
	r.jobQueue = nil
	if cap(r.vm.stack) > maxRetainedStackCap {
		r.vm.stack = nil
		return
	}
	// Keep the backing array but drop every reference so nothing on the
	// stack outlives the call.
	st := r.vm.stack[:cap(r.vm.stack)]
	clear(st)
	r.vm.stack = st[:0]
}
```

(Keep whatever comment already precedes `leave()` if it differs from the one shown; only the constant and the tail of the function body change.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd /home/lk/Work/goja && go test -run 'TestCallableRetainsValueStack|TestLeaveDropsOversizedValueStack' -v . 2>&1 | tail -6`
Expected: both `--- PASS`.

- [ ] **Step 5: Run the whole suite and static checks**

Run:
```bash
cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | tail -6
```
Expected: `gofmt -l` prints nothing; vet is silent; all packages `ok` including tc39.

- [ ] **Step 6: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkCallJS$|BenchmarkFuncCall$' -benchmem -count=10 . > /tmp/goja-bench/task1.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/base.txt /tmp/goja-bench/task1.txt
```
Expected: `BenchmarkFuncCall` and `BenchmarkCallJS` show **lower** `allocs/op` (FuncCall drops by exactly 1 alloc/op; CallJS drops by at least 1) and a negative time delta. Paste the benchstat table into the commit message.

- [ ] **Step 7: Commit**

```bash
cd /home/lk/Work/goja && git add runtime.go runtime_test.go && git commit -F - <<'MSG'
Retain the VM value stack between top-level calls

Runtime.leave() used to set vm.stack to nil, so every call into the
runtime from Go reallocated and re-grew the value stack. Keep the backing
array (cleared) when its capacity is at most maxRetainedStackCap; drop it
above that so a single deep call cannot pin memory.

A no-argument Callable invocation from Go is now allocation-free.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 2: `getLength` instruction for `x.length` reads (spec §4)

**Files:**
- Modify: `vm.go` — add the instruction immediately after `getProp.exec` (currently ends at line 2531)
- Modify: `compiler_expr.go:909-916` (`compiledDotExpr.emitGetter`)
- Test: `compiler_test.go` (append), `builtin_string_test.go` (append)

**Interfaces:**
- Produces: `type getLength struct{}` implementing `instruction` in `vm.go`. Only the compiler emits it.

- [ ] **Step 1: Write the failing compiler test**

Append to `compiler_test.go`:

```go
func TestGetLengthEmission(t *testing.T) {
	count := func(prg *Program) (lengths, getProps int) {
		for _, ins := range prg.code {
			switch ins := ins.(type) {
			case getLength:
				lengths++
			case getProp:
				if unistring.String(ins) == "length" {
					getProps++
				}
			}
		}
		return
	}

	l, g := count(MustCompile("", "s.length", false))
	if l != 1 || g != 0 {
		t.Fatalf("plain read: getLength=%d getProp(length)=%d, want 1/0", l, g)
	}

	l, g = count(MustCompile("", "s.length++", false))
	if l != 0 || g != 1 {
		t.Fatalf("update: getLength=%d getProp(length)=%d, want 0/1", l, g)
	}

	l, g = count(MustCompile("", "var {length} = s", false))
	if l != 0 || g != 1 {
		t.Fatalf("destructuring: getLength=%d getProp(length)=%d, want 0/1", l, g)
	}
}
```

- [ ] **Step 2: Write the failing behaviour tests**

Append to `builtin_string_test.go`:

```go
func TestStringPrimitiveLength(t *testing.T) {
	const SCRIPT = `
	var r = [];
	r.push("".length);                                  // 0
	r.push("hello".length);                             // 5
	r.push("héllo".length);                        // 5 (BMP)
	r.push("a😀b".length);                    // 4 (astral = 2 code units)
	r.push(goStr.length);                               // 5 (imported from Go)
	r.push(new String("abc").length);                   // 3 (wrapper object, generic path)
	r.push(({get length() { return 42; }}).length);     // 42 (own getter, generic path)
	r.push([1,2,3].length);                             // 3 (array, generic path)
	var msg;
	try { undefined.length; } catch (e) { msg = e.message; }
	r.push(msg);
	try { null.length; } catch (e) { msg = e.message; }
	r.push(msg);                                        // goja reports null as "undefined" here today; keep that
	r.join("|");
	`
	r := New()
	r.Set("goStr", "héllo")
	r.testScript(SCRIPT, asciiString("0|5|5|4|5|3|42|3|Cannot read property 'length' of undefined|Cannot read property 'length' of undefined"), t)
}
```

- [ ] **Step 3: Run both tests to verify they fail**

Run: `cd /home/lk/Work/goja && go test -run 'TestGetLengthEmission|TestStringPrimitiveLength' . 2>&1 | tail -5`
Expected: compile error `undefined: getLength`.

- [ ] **Step 4: Add the instruction**

In `vm.go`, directly after the closing brace of `func (g getProp) exec(vm *vm)`, add:

```go
// getLength is emitted for the plain read `x.length`. When x is a string
// primitive it returns the code-unit count directly instead of going
// through the String wrapper singleton, which costs a map write in
// stringObject.setLength plus a map read in getStr. Any other receiver
// takes the generic getProp path unchanged.
type getLength struct{}

func (getLength) exec(vm *vm) {
	if s, ok := vm.stack[vm.sp-1].(String); ok {
		vm.stack[vm.sp-1] = intToValue(int64(s.Length()))
		vm.pc++
		return
	}
	getProp("length").exec(vm)
}
```

- [ ] **Step 5: Emit it from the compiler**

In `compiler_expr.go`, change `compiledDotExpr.emitGetter` from

```go
func (e *compiledDotExpr) emitGetter(putOnStack bool) {
	e.left.emitGetter(true)
	e.addSrcMap()
	e.c.emit(getProp(e.name))
	if !putOnStack {
		e.c.emit(pop)
	}
}
```

to

```go
func (e *compiledDotExpr) emitGetter(putOnStack bool) {
	e.left.emitGetter(true)
	e.addSrcMap()
	if e.name == "length" {
		e.c.emit(getLength{})
	} else {
		e.c.emit(getProp(e.name))
	}
	if !putOnStack {
		e.c.emit(pop)
	}
}
```

Do not touch `emitUnary` or the destructuring site at `compiler_expr.go:3356`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `cd /home/lk/Work/goja && go test -run 'TestGetLengthEmission|TestStringPrimitiveLength' -v . 2>&1 | tail -6`
Expected: both `--- PASS`. If `TestStringPrimitiveLength` fails only on the imported-string element, check that `r.Set("goStr", "héllo")` produced a `String` value (`r.Get("goStr").(String)` in a debug print) — it must; do not weaken the test.

- [ ] **Step 7: Add the benchmark and capture its baseline**

Append to `builtin_string_test.go`:

```go
func BenchmarkStringPrimitiveLength(b *testing.B) {
	r := New()
	v, err := r.RunString(`(function() {
		var s = "hello world, this is a test string";
		var n = 0;
		for (var i = 0; i < 10000; i++) { n += s.length; }
		return n;
	})`)
	if err != nil {
		b.Fatal(err)
	}
	f, _ := AssertFunction(v)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f(_undefined); err != nil {
			b.Fatal(err)
		}
	}
}
```

Capture the baseline with the instruction disabled, then re-enable:

```bash
cd /home/lk/Work/goja && \
sed -i 's/\tif e.name == "length" {/\tif false \&\& e.name == "length" {/' compiler_expr.go && \
go test -run=NONE -bench='BenchmarkStringPrimitiveLength$' -benchmem -count=10 . > /tmp/goja-bench/task2-base.txt 2>&1 ; \
sed -i 's/\tif false \&\& e.name == "length" {/\tif e.name == "length" {/' compiler_expr.go && \
git diff --stat compiler_expr.go
```
Expected: the final `git diff --stat` shows only the intended edit (the `false &&` is gone).

- [ ] **Step 8: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkStringPrimitiveLength$' -benchmem -count=10 . > /tmp/goja-bench/task2.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/task2-base.txt /tmp/goja-bench/task2.txt
```
Expected: `allocs/op` falls from ≈29 740 to ≈8 073 (the remaining allocations are the accumulator `n` boxing, addressed in Task 4); time delta around −40%.

- [ ] **Step 9: Full suite and static checks**

Run:
```bash
cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | tail -6
```
Expected: clean gofmt/vet; all `ok` including tc39.

- [ ] **Step 10: Commit**

```bash
cd /home/lk/Work/goja && git add vm.go compiler_expr.go compiler_test.go builtin_string_test.go && git commit -F - <<'MSG'
Add getLength instruction for string primitive .length reads

Reading .length on a string primitive went through the String wrapper
singleton: setLength() performed a map write and getStr() a map read on
every access. The compiler now emits getLength for the plain read
`x.length`; it returns the code-unit count directly for String values and
falls back to the generic getProp path for every other receiver, so
wrapper objects, arrays, getters and TypeError messages are unchanged.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 3: Hoist reflect metadata out of `wrapReflectFunc` (spec §5)

**Files:**
- Modify: `runtime.go:1987-2067` (`func (r *Runtime) wrapReflectFunc`)
- Test: `runtime_test.go` (append)

**Interfaces:**
- Consumes: `r.toReflectValue(v Value, dst reflect.Value, ctx *objectExportCtx) error`, `reflectTypeError`, `isUncatchableException`, `r.NewGoError` — all existing.
- Produces: nothing new; signature `wrapReflectFunc(value reflect.Value) func(FunctionCall) Value` is unchanged.

- [ ] **Step 1: Write the failing-by-construction tests**

These tests pass today and must still pass after the rewrite; they pin the behaviours the hoisting could plausibly break (variadic arity handling, missing-argument zero fill, trailing-error unwrapping). Append to `runtime_test.go`:

```go
func TestWrapReflectFuncVariadicArity(t *testing.T) {
	r := New()
	r.Set("count", func(sep string, parts ...string) int {
		return len(parts)
	})
	r.Set("fixed", func(a int, b int) int {
		return a*10 + b
	})
	r.testScript(`
		var res = [];
		res.push(count("-"));              // 0 variadic
		res.push(count("-", "a"));         // 1
		res.push(count("-", "a", "b", "c"));// 3
		res.push(fixed(1, 2));             // 12
		res.push(fixed(1));                // b zero-filled -> 10
		res.push(fixed(1, 2, 3));          // extra ignored -> 12
		res.join(",");
	`, asciiString("0,1,3,12,10,12"), t)
}

func TestWrapReflectFuncTrailingError(t *testing.T) {
	r := New()
	r.Set("ok", func() (int, error) { return 7, nil })
	r.Set("bad", func() (int, error) { return 0, errors.New("boom") })
	r.Set("onlyErr", func() error { return errors.New("only") })
	r.testScript(`
		var res = [ok()];
		try { bad(); res.push("no throw"); } catch (e) { res.push(String(e).indexOf("boom") >= 0); }
		try { onlyErr(); res.push("no throw"); } catch (e) { res.push(String(e).indexOf("only") >= 0); }
		res.join(",");
	`, asciiString("7,true,true"), t)
}
```

Ensure `runtime_test.go` imports `"errors"` (add to the import block if absent).

- [ ] **Step 2: Run the tests to confirm they pass on the current code**

Run: `cd /home/lk/Work/goja && go test -run 'TestWrapReflectFunc' -v . 2>&1 | tail -6`
Expected: both `--- PASS`. (If either fails here, the test is wrong — fix the test, not the code, before continuing.)

- [ ] **Step 3: Rewrite `wrapReflectFunc`**

Replace the entire function in `runtime.go` with:

```go
func (r *Runtime) wrapReflectFunc(value reflect.Value) func(FunctionCall) Value {
	// Everything derived from the function's static type is computed once
	// here rather than on every call.
	typ := value.Type()
	nargs := typ.NumIn()
	isVariadic := typ.IsVariadic()
	inTypes := make([]reflect.Type, nargs)
	for i := range inTypes {
		inTypes[i] = typ.In(i)
	}
	if isVariadic {
		inTypes[nargs-1] = inTypes[nargs-1].Elem()
	}
	numOut := typ.NumOut()
	lastOutIsError := numOut > 0 && typ.Out(numOut-1) == reflectTypeError

	return func(call FunctionCall) Value {
		var in []reflect.Value

		if l := len(call.Arguments); l < nargs {
			// fill missing arguments with zero values
			n := nargs
			if isVariadic {
				n--
			}
			in = make([]reflect.Value, n)
			for i := l; i < n; i++ {
				in[i] = reflect.Zero(inTypes[i])
			}
		} else {
			if l > nargs && !isVariadic {
				l = nargs
			}
			in = make([]reflect.Value, l)
		}

		for i, a := range call.Arguments {
			n := i
			if n >= nargs-1 && isVariadic {
				if n > nargs-1 {
					n = nargs - 1
				}
			} else if n > nargs-1 { // ignore extra arguments
				break
			}
			t := inTypes[n]

			v := reflect.New(t).Elem()
			err := r.toReflectValue(a, v, &objectExportCtx{})
			if err != nil {
				panic(r.NewTypeError("could not convert function call parameter %d: %v", i, err))
			}
			in[i] = v
		}

		out := value.Call(in)
		if len(out) == 0 {
			return _undefined
		}

		if lastOutIsError {
			if last := out[len(out)-1]; !last.IsNil() {
				err := last.Interface().(error)
				if _, ok := err.(*Exception); ok {
					panic(err)
				}
				if isUncatchableException(err) {
					panic(err)
				}
				panic(r.NewGoError(err))
			}
			out = out[:len(out)-1]
		}

		switch len(out) {
		case 0:
			return _undefined
		case 1:
			return r.ToValue(out[0].Interface())
		default:
			s := make([]interface{}, len(out))
			for i, v := range out {
				s[i] = v.Interface()
			}

			return r.ToValue(s)
		}
	}
}
```

Note the per-argument `&objectExportCtx{}` is intentionally kept (spec §5).

- [ ] **Step 4: Run the tests**

Run: `cd /home/lk/Work/goja && go test -run 'TestWrapReflectFunc|TestGoReflect|TestNative|TestGoFunc' -v . 2>&1 | grep -E '^(--- |ok|FAIL)' | sort | uniq -c | head`
Expected: only `--- PASS` lines (and `ok`).

- [ ] **Step 5: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkCallReflect$|BenchmarkCallNative$' -benchmem -count=10 . > /tmp/goja-bench/task3.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/base.txt /tmp/goja-bench/task3.txt
```
Expected: `BenchmarkCallReflect` time improves (≈ −5 to −10%); `BenchmarkCallNative` is unchanged within noise; allocs/op for CallReflect is unchanged or lower (the hoisting removes CPU, not allocations — do not expect an alloc drop here).

- [ ] **Step 6: Full suite and static checks**

Run:
```bash
cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | tail -6
```
Expected: clean; all `ok` including tc39.

- [ ] **Step 7: Commit**

```bash
cd /home/lk/Work/goja && git add runtime.go runtime_test.go && git commit -F - <<'MSG'
Hoist reflect type metadata out of wrapReflectFunc's call path

NumIn, IsVariadic, In(i) and the trailing-error check depend only on the
wrapped function's static type but were re-evaluated on every call.
Compute them once when the wrapper is created.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 4: Pointer-free widened integer cache (spec §3)

**Files:**
- Modify: `value.go:60` (`var intCache [256]Value`) and `value.go:1192-1196` (`func init()`)
- Modify: `vm.go:392-400` (`func intToValue`)
- Test: `vm_test.go` (append)

**Interfaces:**
- Produces: constants `intCacheLo = -1024`, `intCacheHi = 65536`, `intCacheLen`; `type ifaceWords struct{ typ, data uintptr }`; `var intBacking [intCacheLen]valueInt`; `var intCache [intCacheLen]ifaceWords`. Only `intToValue` reads the cache.

The benchmark is added and baselined **before** the tests because the tests reference the new constants and would not compile against the old cache.

- [ ] **Step 1: Add the benchmark (literal bounds, so it compiles against both old and new cache)**

Append to `vm_test.go`:

```go
func BenchmarkIntToValue(b *testing.B) {
	var sink Value
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Straddles 65536, the top of the widened cache, so both the cached
		// and the boxing path are exercised in equal measure.
		sink = intToValue(int64(i&0x1ffff) + 65536 - 0x10000)
	}
	_ = sink
}
```

- [ ] **Step 2: Capture the baseline with the old cache**

Run:
```bash
cd /home/lk/Work/goja && git diff --stat && go test -run=NONE -bench='BenchmarkIntToValue$|BenchmarkVMAdd$|BenchmarkFib$|BenchmarkGoReflectGet$|BenchmarkStringPrimitiveLength$|BenchmarkCallNative$' -benchmem -count=10 . > /tmp/goja-bench/task4-base.txt 2>&1 && tail -2 /tmp/goja-bench/task4-base.txt
```
Expected: `git diff --stat` lists only `vm_test.go`; the file ends with `PASS`/`ok`. `BenchmarkIntToValue` shows ≈1 allocs/op (every value ≥ 256 boxes on the old cache).

- [ ] **Step 3: Write the failing tests**

Append to `vm_test.go`:

```go
func TestIntCacheIdentity(t *testing.T) {
	samples := []int64{
		intCacheLo - 1, intCacheLo, intCacheLo + 1,
		-256, -1, 0, 1, 255, 256, 1000, 65535,
		intCacheHi - 1, intCacheHi, intCacheHi + 1,
		1 << 40, -(1 << 40),
	}
	for _, i := range samples {
		v := intToValue(i)
		if got := v.ToInteger(); got != i {
			t.Fatalf("intToValue(%d).ToInteger() = %d", i, got)
		}
		if !v.StrictEquals(valueInt(i)) {
			t.Fatalf("intToValue(%d) !== valueInt(%d)", i, i)
		}
		if !v.SameAs(intToValue(i)) {
			t.Fatalf("intToValue(%d) not SameAs itself", i)
		}
		if exp, ok := v.Export().(int64); !ok || exp != i {
			t.Fatalf("intToValue(%d).Export() = %v (%T)", i, v.Export(), v.Export())
		}
		if _, ok := v.(valueInt); !ok {
			t.Fatalf("intToValue(%d) has dynamic type %T, want valueInt", i, v)
		}
	}
}

func TestIntCacheIsAllocationFree(t *testing.T) {
	var sink Value
	allocs := testing.AllocsPerRun(1000, func() {
		for i := int64(intCacheLo); i < intCacheHi; i += 997 {
			sink = intToValue(i)
		}
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs for cached ints, got %v", allocs)
	}
	_ = sink
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `cd /home/lk/Work/goja && go test -run 'TestIntCache' . 2>&1 | tail -5`
Expected: compile error `undefined: intCacheLo`.

- [ ] **Step 5: Replace the cache declaration and `init` in `value.go`**

Replace `var intCache [256]Value` with:

```go
// Boxed-integer cache.
//
// Value is an interface, so converting a valueInt to a Value normally heap
// allocates. intToValue serves integers in [intCacheLo, intCacheHi) from a
// prebuilt table instead. The table is deliberately stored as raw interface
// words (uintptr) over a package-level valueInt array so that it contains no
// pointers as far as the garbage collector is concerned; a plain [N]Value
// table of this size was measured to slow allocation-heavy workloads by
// ~40% purely from the GC scanning it as a root on every cycle.
//
// Soundness: a non-empty interface is two words (itab, data). valueInt is
// not pointer-shaped, so the data word points at storage holding the int64.
// Both arrays below are package-level: they are never freed and Go's GC
// does not move them, so the data words stay valid without being visible
// to the GC. The itab captured in init is immortal. There is no
// uintptr->unsafe.Pointer conversion anywhere, so vet's unsafeptr check and
// -race's checkptr are satisfied.
const (
	intCacheLo  = -1024
	intCacheHi  = 65536
	intCacheLen = intCacheHi - intCacheLo
)

// ifaceWords mirrors the runtime layout of a non-empty interface value.
type ifaceWords struct {
	typ  uintptr
	data uintptr
}

var (
	intBacking [intCacheLen]valueInt
	intCache   [intCacheLen]ifaceWords
)
```

Replace the existing `init` that fills `intCache` with:

```go
func init() {
	var proto Value = valueInt(0)
	typ := (*ifaceWords)(unsafe.Pointer(&proto)).typ
	for i := range intCache {
		intBacking[i] = valueInt(int64(i) + intCacheLo)
		intCache[i] = ifaceWords{typ: typ, data: uintptr(unsafe.Pointer(&intBacking[i]))}
	}
}
```

`value.go` already imports `"unsafe"`; confirm with `grep -n '"unsafe"' value.go`.

- [ ] **Step 6: Update `intToValue` in `vm.go`**

Replace:

```go
func intToValue(i int64) Value {
	if idx := 256 + i; idx >= 0 && idx < 256 {
		return intCache[idx]
	}
	if i >= -maxInt && i <= maxInt {
		return valueInt(i)
	}
	return valueFloat(i)
}
```

with:

```go
func intToValue(i int64) Value {
	if i >= intCacheLo && i < intCacheHi {
		return *(*Value)(unsafe.Pointer(&intCache[i-intCacheLo]))
	}
	if i >= -maxInt && i <= maxInt {
		return valueInt(i)
	}
	return valueFloat(i)
}
```

Add `"unsafe"` to `vm.go`'s import block (it is not imported there today).

- [ ] **Step 7: Run the tests, including under the race detector**

Run:
```bash
cd /home/lk/Work/goja && go test -run 'TestIntCache' -v . 2>&1 | tail -6 && go test -race -run 'TestIntCacheIdentity' . 2>&1 | tail -3
```
Expected: both tests `--- PASS` in normal mode; `ok` under `-race` with no `checkptr` panic. (`TestIntCacheIsAllocationFree` is excluded from the race run because instrumentation can add allocations; the non-race run is authoritative for it.)

- [ ] **Step 8: Measure**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkIntToValue$|BenchmarkVMAdd$|BenchmarkFib$|BenchmarkGoReflectGet$|BenchmarkStringPrimitiveLength$|BenchmarkCallNative$' -benchmem -count=10 . > /tmp/goja-bench/task4.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/task4-base.txt /tmp/goja-bench/task4.txt
```
Expected: `BenchmarkIntToValue` allocs/op drop from ≈1 to ≈0.5 (half the sampled values are above `intCacheHi` by construction); `BenchmarkStringPrimitiveLength` allocs/op fall from ≈8 073 to a small constant (< 100); `BenchmarkFib` allocs drop; **no benchmark regresses in time beyond noise**. If any allocation-heavy benchmark regresses by more than ~10% consistently, the table is being scanned — re-check that `intCache` is `[intCacheLen]ifaceWords` and not `[...]Value`.

- [ ] **Step 9: Full suite, static checks, and a race run of the VM tests**

Run:
```bash
cd /home/lk/Work/goja && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | tail -6 && go test -race -count=1 -run 'TestVM|TestArray|TestString|TestIntCacheIdentity' . 2>&1 | tail -2
```
Expected: clean gofmt/vet; all `ok` including tc39; race run `ok`.

- [ ] **Step 10: Commit**

```bash
cd /home/lk/Work/goja && git add value.go vm.go vm_test.go && git commit -F - <<'MSG'
Widen the boxed-integer cache and keep it out of the GC root set

The old intCache covered only -256..-1 (the index test was true only for
negative values); non-negative ints below 256 avoided allocation only via
the runtime's convT64 table and everything else allocated. The cache now
covers [-1024, 65536) and is stored as raw interface words over a
package-level valueInt array so the GC never scans it: a plain [N]Value
table of this size was measured to cost ~40% on allocation-heavy scripts
from root scanning alone.

Adds ~1.6 MiB of BSS per process; the range is a tuning constant.

<paste benchstat table here>

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
MSG
```

---

### Task 5: Final verification and hand-off

**Files:**
- Modify: `docs/superpowers/specs/2026-09-16-interop-hot-path-batch1-design.md` (status line only)

**Interfaces:** none.

- [ ] **Step 1: Full clean-tree verification**

Run:
```bash
cd /home/lk/Work/goja && git status --porcelain && gofmt -l . ; go vet ./... && go test -count=1 ./... 2>&1 | tail -8
```
Expected: `git status` prints nothing (no stray files); gofmt/vet clean; all packages `ok`; tc39 ran.

- [ ] **Step 2: Whole-branch benchmark summary against master**

Run:
```bash
cd /home/lk/Work/goja && go test -run=NONE -bench='BenchmarkCallJS$|BenchmarkFuncCall$|BenchmarkCallReflect$|BenchmarkCallNative$|BenchmarkVMAdd$|BenchmarkFib$|BenchmarkGoReflectGet$' -benchmem -count=10 . > /tmp/goja-bench/final.txt 2>&1 && ~/go/bin/benchstat /tmp/goja-bench/base.txt /tmp/goja-bench/final.txt | tee /tmp/goja-bench/summary.txt
```
Expected: every row's `allocs/op` is equal or lower than master; no `sec/op` row shows a statistically significant regression (benchstat prints `~` for non-significant deltas). Any significant regression blocks the hand-off — bisect by reverting one task's commit at a time.

- [ ] **Step 3: Mark the spec as implemented and commit**

Change the `**Status:**` line in the spec to `implemented on branch perf/interop-hot-path-batch1 (2026-09-16); see commit messages for per-change benchmarks`, then:

```bash
cd /home/lk/Work/goja && git add docs/superpowers/specs/2026-09-16-interop-hot-path-batch1-design.md && git commit -q -m "Mark interop hot path batch 1 spec as implemented

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>" && git log --oneline master..HEAD
```
Expected: six commits on the branch (spec, four changes, status update).

- [ ] **Step 4: Report**

Paste `/tmp/goja-bench/summary.txt` and the list of commits to the user. Do **not** push or open a PR unless asked.
