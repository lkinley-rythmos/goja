# Goja batch 2 — iterator fast path, own-data read API, regex cache

**Date:** 2026-09-16
**Status:** draft, awaiting review
**Branch:** `perf/iterator-fast-path-batch2` (from `master` at the batch-1 merge)

## 1. Motivation and evidence

Batch 1 (`docs/superpowers/specs/2026-09-16-interop-hot-path-batch1-design.md`)
was qualified against the real GAP Get Profile workload in
`rcx-go-crud` (`docs/GOJA-GAP-FORK-RESULTS.md` there) and produced no
material change: it optimized integer boxing, Go→JS call entry, string
`.length` and reflect-wrapped calls, none of which that workload spends
time on.

The workload's own profiles (`selector-off-profile-candidate`, CA, 40
requests, ~60 ms CPU/request) show where the time is:

| allocation source (1.01 GB sampled over 40 requests) | share |
|---|---|
| Iterator protocol result objects — `createIterResultObject` and the Go maps they allocate, reached via `iteratorRecord.step` (85% `for‑of`, 15% array destructuring) and `iteratorRecord.iterate` (84% array spread, 16% `Object.fromEntries`) — 94.6% from `arrayIterObject.next`, 5.4% from `regExpStringIterObject.next` | **~55%** |
| Per-array / per-function property maps (`arrayObject.init`, `baseFuncObject.init`) | ~23% |
| Runtime regex compilation — `String.prototype.matchAll`'s per-call species clone (6.9%) and `RegExp()` calls (1.9%) | ~9% |
| `Object.getOwnPropertyDescriptor` descriptor objects from the host bridge's `ownDataValue` | ~7% |

CPU: `mallocgc` + GC ≈ 33% of samples; the iterator protocol itself
(`iteratorRecord.iterate` 12%, `step`/`iterNext` 5.5%,
`createIterResultObject` 4%) ≈ 18%.

This batch removes the first, third and fourth rows. The second row
(map-free arrays and functions) is a separate object-model change and
is deferred to its own spec, to be justified by a fresh profile after
this batch lands.

## 2. Scope

In scope, as four independent commits:

- **A.** Intrinsic-iterator fast path in `iteratorRecord`, applied to `*arrayIterObject`.
- **B.** The same fast path applied to `*regExpStringIterObject`.
- **C.** `func (o *Object) GetOwnDataValue(name string) (Value, bool)` — a non-allocating own data property read.
- **D.** A bounded, process-global cache of compiled regex patterns in `compileRegexp`.

Out of scope: map-free arrays/functions; Map/Set/String iterators and
generators (each is a ~10-line follow-up on the same mechanism, to be
triggered by a profile that shows them); `regExpExec` match-result
allocations (8.3% of bytes; next regex candidate); a public knob for
the regex cache size; any change to `rcx-go-crud` (an app-side patch is
provided in §8 for the user to apply within their own qualification
process).

## 3. Change A — intrinsic iterator fast path

### 3.1 Anchors

`createArrayIterProto` (builtin_array.go) and
`createRegExpStringIteratorPrototype` (builtin_regexp.go) create the
native `next` function objects with `r.newNativeFunc(...)`. Those two
`*Object`s are stored in new `r.global` fields, `arrayIterProtoNext`
and `regExpStringIterProtoNext`. They are the only objects for which
the fast path is valid. They are created lazily with their prototypes;
until then the fields are nil and never match.

### 3.2 Record and gate

```go
type iterNexter interface {
	// nextValue performs exactly the observable work of the intrinsic
	// next() — same reads, same order, same state transitions — without
	// materializing the {value, done} result object.
	nextValue() (value Value, done bool)
}

type iteratorRecord struct {
	iterator *Object
	next     func(FunctionCall) Value
	fast     iterNexter // non-nil only when §3.2's gate passed
}
```

In `getIterator`, after `next` is resolved from `iter.self.getStr("next", nil)`:

- if the resolved `next` object is pointer-equal to `r.global.arrayIterProtoNext` **and** `iter.self` is a `*arrayIterObject`, set `fast` to it;
- else if it is pointer-equal to `r.global.regExpStringIterProtoNext` **and** `iter.self` is a `*regExpStringIterObject`, set `fast` to it;
- otherwise leave `fast` nil.

Both conditions are required: `%ArrayIteratorPrototype%.next.call(mapIterator)`
must keep taking the generic path and throwing the spec'd TypeError.

### 3.3 Consumers

`iteratorRecord.step` and `iteratorRecord.iterate` are the only two
consumers of `next`. In each, the body that today does
`res := r.toObject(ir.next(FunctionCall{This: ir.iterator}))`, then
`iteratorComplete(res)` / `iteratorValue(res)`, becomes:

```go
if ir.fast != nil {
	value, done = ir.fast.nextValue()
} else {
	res := r.toObject(ir.next(FunctionCall{This: ir.iterator}))
	done = iteratorComplete(res)
	if !done {
		value = iteratorValue(res)
	}
}
```

inside the same `vm.try` / `tryFunc` wrappers as today, so exceptions
raised by getters or detachment checks propagate identically. `close()`,
`returnIter()`, and the VM `iterStack` bookkeeping in `iterNext`,
`iterGetNextOrUndef`, `_pushArraySpread` and `_iterate` are untouched.
Every consumer — `for-of`, spread, destructuring, `Array.from`,
`Promise.all`, `new Map/Set(iterable)`, `Object.fromEntries`,
`Runtime.ForOf` — inherits the fast path through these two functions.

### 3.4 Invalidation (by construction)

| tampering | why the generic path is taken |
|---|---|
| `Array.prototype[Symbol.iterator] = f` | `f` returns a non-`*arrayIterObject` |
| `%ArrayIteratorPrototype%.next = f` | resolved `next` ≠ anchor |
| own `next` on the iterator instance | shadows the prototype; resolved ≠ anchor |
| accessor installed for `next` on the prototype | resolved value is the getter's result, ≠ anchor |
| user iterator or generator | `iter.self` is not an intrinsic type |
| iterator created by another `Runtime` | anchors are per-runtime, compared by pointer |

There are no flags to maintain and no global "protector" state; the
check is two pointer comparisons per `getIterator`.

### 3.5 `arrayIterObject` (array.go)

`next()` is split so there is exactly one body:

```go
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

This is today's `next()` with the wrapper factored out: the `length`
read, the element read, the typed-array detachment check and the
`nextIdx`/`obj` transitions are unchanged. `entries()` still allocates
its observable `[index, value]` pair. `createIterResultObject` itself is
unchanged and continues to serve generators, direct `iter.next()`
calls, and every tampered path.

### 3.6 Observable equivalence

Per ECMA-262 `IteratorStep`, the observable effects of calling the
intrinsic `%ArrayIteratorPrototype%.next` are the reads it performs on
the iterated object; the result object is a fresh ordinary object whose
own `value`/`done` properties are read immediately by the caller and
never exposed to user code when the caller is `step`/`iterate`. Since
`nextValue()` is the same body, effects and their order are identical;
only the unobservable allocation disappears.

## 4. Change B — regexp-string iterator

`regExpStringIterObject.next()` (builtin_regexp.go) is split the same
way: `nextValue()` holds the existing body — the `done` latch, the
`regExpExec` call, the empty-match `lastIndex` advance via
`advanceStringIndex64` — and `next()` wraps it with
`createIterResultObject`. `getIterator`'s gate (§3.2) recognises the
pair. The per-step `regExpExec` match array remains (it is observable);
only the `{value, done}` wrapper is removed.

## 5. Change C — `GetOwnDataValue`

In value.go, beside `Get`, `Keys`, `DefineDataProperty`:

```go
// GetOwnDataValue returns the value of the object's own data property
// `name` without invoking accessors and without allocating a property
// descriptor. ok is false when the property is absent or is an accessor
// property. Exotic objects behave as for Object.getOwnPropertyDescriptor:
// a Proxy's getOwnPropertyDescriptor trap still runs.
func (o *Object) GetOwnDataValue(name string) (v Value, ok bool)
```

Implementation: `p := o.getOwnProp(newStringValue(name))` (the existing
internal resolver, so index-shaped names on arrays and Proxy traps
behave exactly as `getOwnPropertyDescriptor`), then:

- `p == nil` → `(nil, false)`;
- `p.(*valueProperty)` with `accessor` → `(nil, false)`;
- `p.(*valueProperty)` data → `(p.value, true)`;
- any other `Value` (a plain data slot) → `(p, true)`.

`(Undefined, true)` is distinguishable from `(nil, false)` so callers
can tell "own data property holding undefined" from "absent".

## 6. Change D — bounded global regex compile cache

In builtin_regexp.go, `compileRegexp(patternStr, flags string)` is the
single choke point behind the runtime path (`new RegExp`, `RegExp()`,
`matchAll`'s species clone, string-pattern `split`/`replace`) and the
compiler's literal path.

```go
type regexpCacheKey struct{ src, flags string }

var regexpCache = struct {
	sync.Mutex
	m map[regexpCacheKey]*regexpPattern
}{m: make(map[regexpCacheKey]*regexpPattern)}

const regexpCacheMaxEntries = 1024
```

- Lookup under the mutex; on hit return `template.clone()`.
- On miss, compile outside the lock with today's code; on success,
  under the mutex, if `len(m) >= regexpCacheMaxEntries` replace the map
  with a fresh one, then insert. Concurrent compiles of the same key
  both succeed; the last insert wins.
- Errors are not cached.
- The key uses the pattern string as passed (after
  `escapeInvalidUtf16` on the runtime path) and the raw flags string;
  differently ordered flags simply miss.

Soundness: the template is never handed out; every caller receives a
`clone()`, exactly as the literal path already does per `Program`.
`clone()` shares the compiled `*regexp.Regexp` (goroutine-safe by Go's
contract) and `*regexp2.Regexp` (documented safe for concurrent use;
runners are pooled under its own mutex) and gives the clone a fresh nil
match cache. Goja already depends on this sharing whenever one `Program`
runs in several `Runtime`s on different goroutines, so no new
concurrency assumption is introduced. Mutable per-object state
(`lastIndex`) lives on the `regexpObject`. The bound makes memory
O(cap × pattern); clear-on-cap can thrash under adversarial dynamic
patterns but never does worse than today.

## 7. Testing and acceptance

Correctness (goja package, run with test262 checked out; `-race` on
new tests):

- Fast path engaged: `for-of`, `[...a]`, `[x, y] = a`, `Array.from`,
  `new Map(entries)`, `Runtime.ForOf`; over plain, sparse (holes), and
  typed arrays; `keys()`/`entries()` kinds; a `length` getter that
  throws (exception text and `iterStack` state identical); an
  array-like Proxy whose trap-call sequence is recorded and compared
  against the generic path; early `break`; `matchAll` loops with
  global, sticky and empty-match patterns.
- Fast path not engaged, each asserting the tampering's observable
  effect: replaced `Array.prototype[Symbol.iterator]`; replaced
  `%ArrayIteratorPrototype%.next` that counts its calls; own `next` on
  an instance; accessor `next`; `%ArrayIteratorPrototype%.next.call(mapIterator)`
  throws TypeError; generators and user iterators; iterator from a
  second `Runtime`.
- `GetOwnDataValue`: plain object, array index, absent, accessor,
  non-enumerable, Proxy with a `getOwnPropertyDescriptor` trap (trap
  observed), Go-reflect-backed object; `(Undefined, true)` vs `(nil, false)`.
- Regex cache: same source+flags in two Runtimes have independent
  `lastIndex`; a hit returns a clone whose match cache does not leak
  into another object; cap overflow resets and still compiles;
  concurrent compiles from multiple goroutines under `-race`; invalid
  pattern not cached and re-reported.

Benchmarks (goja package; `allocs/op` is the acceptance number, time is
directional under the batch-1 noisy-machine rule):

- `BenchmarkForOfArray`, `BenchmarkArraySpread`, `BenchmarkArrayDestructure`,
  `BenchmarkArrayFrom` over a 1000-element array: allocs/op falls from
  ≈ one per element to a small constant (< 10).
- `BenchmarkMatchAllLoop`, `BenchmarkRegExpCtorRepeated`: compilation
  allocations gone after the first call.
- `BenchmarkGetOwnDataValue`: 0 allocs/op versus the descriptor path.

Expected figures in the plan are to be measured on this codebase, not
carried over from a spike (the batch-1 lesson). The user's GAP harness
in `rcx-go-crud` is the real acceptance gate: the branch is pushed to
the fork and pinned by commit for the alternate module; merge to
`master` follows qualification.

## 8. App-side patch (for the user to apply; not part of this branch)

`internal/v8engine/goja_context.go` in `rcx-go-crud`:

```go
func (g *GojaSession) ownDataValue(obj *goja.Object, key string) (goja.Value, error) {
	if v, ok := obj.GetOwnDataValue(key); ok {
		return v, nil
	}
	return goja.Undefined(), nil
}
```

Signature and `error` return unchanged; `g.descriptor` remains in use
by the date path only.

## 9. Risks

1. A hidden observable difference between `next()` and `nextValue()` —
   mitigated structurally (one body) and by the Proxy trap-order test;
   test262 covers the iterator protocol extensively.
2. Anchor lifecycle — anchors are created with their prototypes and
   compared by pointer; a nil anchor never matches. No ordering hazard.
3. Regex cache memory — bounded by the cap; degrades to today's
   behaviour under thrash.
4. Generators and user iterators see no benefit; deliberate (§2).

## 10. Sequencing

Independent commits in this order: A (mechanism + array iterator), B
(regexp-string iterator), C (`GetOwnDataValue`), D (regex cache). Each
carries its tests and its benchmark delta in the commit message.
