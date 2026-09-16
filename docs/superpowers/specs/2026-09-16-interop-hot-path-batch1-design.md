# Goja embedded-interop hot path — batch 1 design

**Date:** 2026-09-16
**Status:** draft, awaiting review
**Scope:** four self-contained runtime changes that reduce per-call cost and
allocation volume when goja is embedded in a Go service that makes many
Go→JS and JS→Go calls. Each change is independently landable and
independently measurable.

Explicitly **out of scope** (each is its own design): inline caches / hidden
classes for property and global lookup, replacing the `Value` interface with
a tagged/unboxed representation, and restructuring the interpreter dispatch
loop. Those are where the remaining large wins live, but they are
architectural rewrites; this batch is the cheap, low-risk tier that should
land first.

## 1. Background and evidence

A throwaway benchmark suite (Go→JS `Callable` invocations, JS→Go native and
reflect-wrapped calls, JS reading Go structs/maps, per-request runtime
creation, and pure-JS loops) was profiled with `pprof` for CPU and
allocations against `master` at `b3fa021`. Numbers below are minimum-of-N
from interleaved base/experiment runs on an i7-9750H (a thermally
throttling laptop, hence min and median are both reported in the plan's
acceptance criteria).

Findings that this batch addresses:

| # | Observation | Evidence |
|---|---|---|
| A | The VM value stack is discarded on every return to Go (`Runtime.leave`, `runtime.go:2851` sets `r.vm.stack = nil`), so the next `Callable` invocation reallocates and re-grows it. | `valueStack.expand` = 99.4% of allocations and 22% of CPU for a no-arg Go→JS call. |
| B | `Value` is an interface; `valueInt` boxes on conversion. The existing `intCache` (`value.go:60`, `vm.go:392`) only covers **-256..-1** — the index test `idx := 256 + i; idx >= 0 && idx < 256` is true only for negative `i`. 0..255 avoid allocation only via Go's `convT64` static table. Every other integer result allocates 8 bytes. | `intToValue` = 99.8% of allocations in a property-access loop; `MethodCall` 19 694 allocs/op. |
| C | `str.length` on a primitive fetches the runtime string singleton, calls `stringObject.setLength()` which does a **map write** (`string.go:153–159`), then `getStr` attempts an index parse and does a **map read**. | `asciiString.baseObject` 14% of CPU when reading `.length` on Go-struct string fields; `StringLength` bench 29 740 allocs/op. |
| D | `wrapReflectFunc` (`runtime.go:1987`) re-derives `typ.NumIn()`, `typ.IsVariadic()`, `typ.In(i)`, `typ.Out(n-1) == reflectTypeError` on every call, though all are fixed at wrap time. | `reflect.Value.call` 33%, `wrapReflectFunc.func1` 7.6% flat, `reflect.funcLayout` 6.8% for a `func(int64) int64`. |

Measured effect of the four prototypes combined (unit tests passing;
test262 **not** run — see §7):

| benchmark | Δ min | Δ median | allocs/op |
|---|---|---|---|
| Go→JS no-arg call | −32% | −49% | 1 → 0 |
| Go→JS 3-arg call | −23% | −16% | 2 → 1 |
| JS reads Go map fields | −32% | −19% | 3724 → 1000 |
| JS reads Go struct fields | −18% | −23% | 8729 → 5000 |
| JS `s.length` loop | −41% | −42% | 29 740 → 8073 |
| JS prototype method call | −19% | −6% | 19 694 → 31 |
| JS→Go reflect-wrapped call | −9% | −9% | 7212 → 5382 |
| per-request `New()`+run | −15% | −11% | 132 → 50 |

## 2. Change A — retain the VM value stack across top-level calls

**File:** `runtime.go`, `func (r *Runtime) leave()`.

**Current:** `r.vm.stack = nil` after draining the job queue.

**New:** keep the backing array when its capacity is at most
`maxRetainedStackCap` (a new package constant, 1024 slots = 16 KiB of
interface headers); clear every slot to `nil` so no JS value is kept alive
across calls; reslice to length 0. Above the threshold drop it as today, so
a single pathological deep call cannot pin a large array for the runtime's
lifetime.

```go
const maxRetainedStackCap = 1024

func (r *Runtime) leave() {
	// ... drain jobQueue unchanged ...
	r.jobQueue = nil
	if cap(r.vm.stack) > maxRetainedStackCap {
		r.vm.stack = nil
		return
	}
	st := r.vm.stack[:cap(r.vm.stack)]
	clear(st)
	r.vm.stack = st[:0]
}
```

**Why it works:** `valueStack.expand` (`vm.go:442`) reslices without
allocating whenever `idx < cap`. Retaining capacity turns the per-call
`make`+`copy` into a reslice.

**Semantics:** none observable. `leave()` runs only when `len(callStack)
== 0`, i.e. control is leaving the runtime; every live value on the stack
has already been consumed. Clearing slots preserves the current GC
behaviour (nothing retained between calls). `leaveAbrupt()` is unchanged.

**Memory:** at most 16 KiB retained per `Runtime`. With one runtime per
goroutine or per request this is negligible; with thousands of idle
runtimes it is still bounded and small.

## 3. Change B — pointer-free widened integer cache

**Files:** `value.go` (cache declaration and `init`), `vm.go`
(`intToValue`).

**Range:** `[-1024, 65536)`. Covers negative small ints (the existing
−256..−1 plus headroom) and the full `uint16` range, which captures array
indices, string lengths, char codes, HTTP status codes, ports, and most
loop counters and accumulated sums seen in service-style scripts. 66 560
entries.

**Layout — the important part.** A naive `var intCache [N]Value` is
rejected: it was prototyped and **regressed** an allocation-heavy benchmark
by +44%, and the regression vanished under `GOGC=off`, proving the cause
was the GC scanning ~130 K pointer words of package-level roots on every
cycle. The cache must be pointer-free from the GC's point of view:

```go
// value.go
const (
	intCacheLo  = -1024
	intCacheHi  = 65536
	intCacheLen = intCacheHi - intCacheLo
)

// ifaceWords mirrors the runtime layout of a non-empty interface value.
// Holding the two words as uintptr keeps intCache out of the GC root set;
// the pointees are package-level arrays that are never freed or moved.
type ifaceWords struct {
	typ  uintptr
	data uintptr
}

var (
	intBacking [intCacheLen]valueInt
	intCache   [intCacheLen]ifaceWords
)

func init() {
	var proto Value = valueInt(0)
	typ := (*ifaceWords)(unsafe.Pointer(&proto)).typ
	for i := range intCache {
		intBacking[i] = valueInt(int64(i) + intCacheLo)
		intCache[i] = ifaceWords{typ: typ, data: uintptr(unsafe.Pointer(&intBacking[i]))}
	}
}

// vm.go
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

**Soundness argument (to be kept as a comment in the code):**

- A Go non-empty interface is two machine words: itab pointer, data
  pointer. `valueInt` is not pointer-shaped, so the data word points at
  storage holding the `int64`.
- Both arrays are package-level; Go's GC is non-moving and never frees
  globals, so the `data` word is valid for the process lifetime without
  being visible to the GC.
- The itab for `(Value, valueInt)` is immortal; capturing it once from a
  real interface value is exactly how the compiler would produce it.
- The only `unsafe.Pointer` conversions are pointer→`uintptr` (storing) and
  `*ifaceWords`→`*Value` (same size, same alignment). No
  `uintptr`→`unsafe.Pointer` conversion exists, so `go vet`'s `unsafeptr`
  check and `-race`'s `checkptr` instrumentation are satisfied. The
  repository already uses `unsafe` in `value.go`, `string_unicode.go`, and
  `typedarrays.go`.

**Cost:** ~1.6 MiB of BSS per process (not per runtime): 66 560 × (8 + 16)
bytes, zero GC scan cost, ~0.2 ms one-time `init`. The range is a tuning
constant; the plan includes re-measuring `[-1024, 8192)` as a smaller
fallback if the footprint is judged too large.

**Semantics:** none. `intToValue` returns the same `valueInt` values; only
their boxing differs. `valueInt` values are compared by value everywhere
(`SameAs`, `StrictEquals`, `Equals`), never by interface data pointer, so
sharing boxes is invisible.

## 4. Change C — `length` fast path for string primitives

**Files:** `vm.go` (new instruction), `compiler_expr.go`
(`compiledDotExpr.emitGetter`, line 912).

**Design:** a dedicated instruction rather than a name check inside
`getProp.exec`, so that the ~99% of property reads that are not `.length`
pay nothing.

```go
// vm.go
// getLength is emitted for the read `x.length`. It short-circuits the case
// where x is a string primitive, which would otherwise round-trip through
// the String wrapper singleton (a map write in setLength plus a map read).
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

```go
// compiler_expr.go, compiledDotExpr.emitGetter
if e.name == "length" {
	e.c.emit(getLength{})
} else {
	e.c.emit(getProp(e.name))
}
```

Only the plain read site is changed. The `emitUnary` sites (`s.length++`),
the destructuring site (`compiler_expr.go:3356`), `getPropRecv*`,
`getPropCallee*`, and `getPropRef*` keep the generic path — they are either
never a primitive-string read or are followed by a write whose error
semantics must stay untouched.

**Semantics:** for a String primitive `v`, ECMAScript evaluates
`v.length` as `ToObject(v).[[Get]]("length")`; the String exotic object
owns a non-writable, non-configurable `length` equal to the UTF-16 code
unit count, and own properties shadow `String.prototype`, so no user code
can intercept it. All three `String` implementations (`asciiString`,
`unicodeString`, `*importedString`) return the code-unit count from
`Length()`; `*importedString.Length()` performs its lazy scan exactly as
the existing path does via `setLength`. Non-string receivers (including
arrays, `String` wrapper *objects*, proxies, and Go-reflected values) fall
through to the unchanged `getProp` code, so their observable behaviour,
including getter invocation order and `TypeError` messages for
`undefined`/`null`, is identical.

**Deliberately not included:** an `*arrayObject` case. Arrays already reach
their length via `getOwnPropStr` without a map access; adding a case here
is cheap but is an independent, separately measurable change and is left
for follow-up.

## 5. Change D — hoist reflect metadata out of the reflect call wrapper

**File:** `runtime.go`, `wrapReflectFunc`.

Compute once in the outer function and capture in the closure:

- `typ := value.Type()`, `nargs := typ.NumIn()`, `isVariadic := typ.IsVariadic()`
- `inTypes []reflect.Type` with `inTypes[nargs-1]` already replaced by its
  `Elem()` when variadic, so the per-argument branch becomes a slice
  index.
- `lastOutIsError := typ.NumOut() > 0 && typ.Out(typ.NumOut()-1) == reflectTypeError`

The per-call body keeps its structure (fill missing args with
`reflect.Zero`, truncate extras, convert each argument via
`toReflectValue`, `value.Call`, unwrap trailing error, `ToValue` the
results). Per-argument `objectExportCtx` allocation is **retained**: the
context caches exported objects for cycle/identity handling within one
conversion, and sharing one across arguments would change the Go values
produced when the same JS object is passed twice. That is a semantic
change and is out of scope.

**Semantics:** none. Every hoisted quantity is a pure function of the
static Go function type.

## 6. Testing

Per change, in goja's own test files (not the throwaway suite):

- **A:** `TestCallableRetainsStack` — `testing.AllocsPerRun` on a no-arg
  `Callable` from Go asserts `0` allocations after warm-up; a second test
  drives the stack past `maxRetainedStackCap` (deep recursion) and asserts
  `cap(r.vm.stack) == 0` after return, then that a subsequent call still
  succeeds. Existing `BenchmarkCallJS`/`BenchmarkFuncCall` remain the
  performance guard.
- **B:** `TestIntCacheIdentity` — for a sample of values across and just
  outside `[intCacheLo, intCacheHi)`, `intToValue(i).ToInteger() == i`,
  `StrictEquals` holds against a freshly boxed `valueInt(i)`, and
  `Export()` returns `int64(i)`. Run under `-race` (the repository already
  has `tc39_race_test.go`) to exercise `checkptr`. Benchmark:
  `BenchmarkIntToValue` over a range straddling the cache edge.
- **C:** `TestStringLengthFastPath` — ascii, unicode (with astral
  characters, asserting code-unit count), imported strings, empty string;
  `String` wrapper objects and objects with a user `length` getter to
  prove the fallback; `undefined.length` still throws the same
  `TypeError` text. Compiler test asserting `getLength` is emitted for
  `x.length` and `getProp` for `x.length++`.
- **D:** existing `TestGoReflect*`/`TestNative*` coverage plus one new
  test for a variadic Go function with fewer, exact, and extra JS
  arguments, and one for `func(...) (T, error)` returning a non-nil error.
- **All:** `gofmt`, `go vet ./...`, staticcheck per `staticcheck.conf`,
  `go test ./...` with test262 checked out (§7), and the interleaved A/B
  harness re-run per change with the acceptance thresholds in the plan.

## 7. Risks and prerequisites

1. **test262 has not been run.** `testdata/test262` is absent locally, so
   the tc39 suite skipped during prototyping; CI runs it via
   `.tc39_test262_checkout.sh`. Running it locally before the first commit
   is a hard prerequisite — changes B and C touch value and property
   semantics at the edges.
2. **`unsafe` in change B.** Contained to ~20 lines with a written
   soundness argument; must be reviewed by someone comfortable with Go's
   interface layout. If the reviewer rejects it, the fallback is a plain
   `[N]Value` array with a small `N` (≤ 8192) where the GC-root cost was
   measured as negligible.
3. **Measurement noise.** The prototyping machine throttles; the plan's
   acceptance criteria use interleaved runs and both min and median. A
   quieter machine should be used for the final numbers.
4. **Static footprint of change B** (~1.6 MiB/process) may matter for
   very small deployments; it is a two-constant tuning knob.

## 8. Sequencing

Independent commits in this order, each with its own benchmark delta in
the commit message: **A** (smallest, highest value for the embedded use
case) → **C** → **D** → **B** (most review surface). A single PR per
change, or one PR with four commits, at the maintainer's preference.
