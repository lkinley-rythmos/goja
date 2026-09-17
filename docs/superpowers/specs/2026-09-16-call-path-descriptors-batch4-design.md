# Goja batch 4 — virtual descriptor objects, in-place call/apply/bind, micro allocations, pre-sized objects

**Date:** 2026-09-16
**Status:** implemented on branch perf/call-path-batch4 (2026-09-16); per-change benchmarks in commit messages
**Branch:** `perf/call-path-batch4` (from `master` at `50ffab1`)

## 1. Motivation and evidence

Batch 3 (`docs/superpowers/specs/2026-09-16-object-model-batch3-design.md`)
was qualified on the GAP Get Profile workload in `rcx-go-crud`
(`docs/GOJA-GAP-FORK-BATCH3-RESULTS.md` there): US mean 58.41 → 52.53 ms,
p95 76.45 → 65.87 ms, CPU/request 53.93 → 48.06 ms. The post-batch-3 CA
profile (`.qualification/goja-gap/fork3-profile-fork`, 88 requests,
1446 MB allocated, 2.04 s CPU sampled) shows what remains in the engine:

| Item | Evidence |
|------|----------|
| Descriptor objects from `Object.getOwnPropertyDescriptor` | 244 MB, 16.9% of bytes: 120 MB from the Go bridge's `ownDataValue` (removed by the batch-2 §8 app patch, not yet applied) and 124 MB from JS (`goja_selector_cache.go` `guard()` walks prototype chains calling `descriptor(object, key)`). Per descriptor ≈ 600 B; `NewObject` is 23% of it, the four `setOwnStr` calls 77% (prototype walk to `Object.prototype`, map assign into a growing map, `propNames` growth 1→2→4). Also `setOwnStr` 8.8% and `mapassign_faststr` 7.4% of CPU. |
| `Function.prototype.call` / `apply` / bound functions | `functionproto_call` 8.8% CPU cum, `boundCallable.func1` 2%. Micro-benchmark (fork master, ns per invocation over an inline loop): direct JS→JS call ≈ 150 / 1 alloc; `f.call(null, s, x)` ≈ 590 / 2 allocs; `f.apply` the same; `hop.call(obj, 'k')` +135 ns over `obj.hasOwnProperty('k')`; `Function.prototype.call.bind(hasOwnProperty)` +72 ns and +2 allocs. Builtin→callback re-entry (`find`, `map`) ≈ 130–150 ns / 1 alloc — no worse than a direct call, so try-frame reuse is *not* pursued. |
| `toPropertyKey` | 36 MB, 2.5%: `asciiString.ToString` re-boxes an already-string key. |
| `_defineOwnProperty` | 19.5 MB: `&valueProperty{}` allocated for every new property, then discarded for all-true data descriptors (every bridge `DefineDataProperty` call). |
| `assertCallable` | `f.Call` method value escapes (`go build -gcflags=-m`): 16 B on every `toCallable` / `AssertFunction` of a JS function. |
| Index boxing in array builtins | `valueInt(k)` boxed directly (64 sites in `builtin_array.go`): 8 B per element beyond index 255 in `find`, `map`, `forEach`, … |
| Bridge object growth | 60 MB flat in `defineOwnPropertyStr`: map rehash and `propNames` doubling as the bridge defines properties one by one onto fresh objects whose field count it already knows. |

## 2. Scope

- **A.** Virtual descriptor objects (`descriptorObject`).
- **B1.** In-place stack rewriting for `Function.prototype.call`,
  `Function.prototype.apply` and bound functions when invoked from the VM.
- **C.** Four micro-allocation fixes: `toPropertyKey`, `_defineOwnProperty`
  new-property path, `assertCallable` method-value caching, `intToValue`
  in array builtins.
- **E.** `Runtime.NewObjectWithCapacity(n int) *Object` plus an app-side
  patch (§8).

Deferred, on record: try-frame reuse across builtin callbacks (B2 —
measured not worth it); per-callsite global lookup caching (D,
`templatedObject` ~8% CPU, structural); the `AssertFunction` wrapper's own
16 B per call (changes the public `Callable` closure shape).

## 3. Change A — `descriptorObject`

### Type

```go
type descriptorObject struct {
	baseObject
	value, get, set                    Value // get/set: *Object or _undefined
	accessor                           bool
	writable, enumerable, configurable bool
	materialized                       bool
}
```

`valuePropToDescriptorObject` (`builtin_object.go`) builds one:
`&Object{runtime: r}` whose `self` is a `descriptorObject` with
`class = classObject`, `prototype = r.global.ObjectPrototype`,
`extensible = true`, `values == nil`, `propNames == nil`. `desc == nil`
still returns `_undefined`. `Object.getOwnPropertyDescriptor`,
`Object.getOwnPropertyDescriptors` and `Reflect.getOwnPropertyDescriptor`
all go through this function and therefore all produce the virtual form.

### Virtual key set

In spec order: data form `value, writable, enumerable, configurable`;
accessor form `get, set, enumerable, configurable`. Each is a data
property that is writable, enumerable and configurable, so reads return
the field directly (`valueTrue`/`valueFalse` for flags, `_undefined` for a
missing getter/setter) and never need a `valueProperty`.

### Overrides while un-materialized

Mirroring `baseFuncObject`'s batch-3 intrinsics (`func.go`):
`getStr`, `getOwnPropStr`, `hasOwnPropertyStr`, `setOwnStr` (a write to
one of the four keys updates the field in place with no map; a write to
any other key materializes, then delegates), `setForeignStr` (consults the
virtual set, then delegates as `baseFuncObject` does),
`defineOwnPropertyStr` and `deleteStr` (materialize, then delegate),
`stringKeys`, `iterateStringKeys`, `appendEnumerableStringKeys` (emit the
four names from the fields, then delegate for the rest),
`preventExtensions` (materialize, then delegate). `Idx` variants stay on
`baseObject`: they route through the `Str` methods, so `"0"` is caught by
the same hooks. Symbol keys are untouched (`symValues` is independent).

### Materialize

`ensureMaterialized`: `values = make(map[unistring.String]Value, 5)`,
`propNames = make([]unistring.String, 0, 5)`, `_put` the four keys in
order, `materialized = true`. After that every override takes a
one-branch check and delegates unconditionally to `baseObject`; the
object is then byte-for-byte an ordinary object, so `export`, `equal`,
`Keys`, `AppendKeys`, `GetOwnDataValue`, JSON, `Object.assign`, proxies
and `Object.freeze` see nothing unusual in either state.

### Observable behaviour

Identical to today: `Object.prototype.toString` → `[object Object]`,
`Object.keys` order, `hasOwnProperty`, `in`, `JSON.stringify`,
`Object.getPrototypeOf`, `Object.isExtensible`; `d.value = x`,
`delete d.value`, `Object.defineProperty(d, …)`, `Object.freeze(d)` behave
as on a plain object. The intended differences are memory (≈600 B →
≈230 B per descriptor) and no map operations on create or read.

## 4. Change B1 — in-place `call` / `apply` / bound invocation

### Today

`f.call(t, a)` from JS runs `call.exec` → `nativeFuncObject.vmCall`
(push ctx) → `functionproto_call` → `toCallable` (allocates the `f.Call`
method value) → `__call` (copies args, pushes a try frame, pushes a second
ctx, runs a nested `vm.run` loop, pops both).

### Design

The VM dispatches every call through `obj.self.vmCall(vm, n)` with the
stack window laid out as `[this][callee][arg0 … arg(n-1)]`
(`this` at `sp-n-2`, `callee` at `sp-n-1`). Three impl types get their
own `vmCall` that rewrites that window in place and then invokes the
*target's* `vmCall` — no nested run loop, no try frame, no extra ctx. The
Go-facing `assertCallable`/`Call` paths of all three are untouched, so Go
callers and their exception semantics do not change.

1. **`funcProtoCallObject`** (embeds `nativeFuncObject`; used only for the
   `Function.prototype.call` intrinsic). `vmCall(vm, n)`: target `f` =
   `stack[sp-n-2]`. If `f` is an `*Object` whose `self` is a
   `*baseJsFuncObject`, `*arrowFuncObject`, `*nativeFuncObject` with
   `f != nil`, or `*boundFuncObject` → rewrite `[f][call][t][a1 …]` to
   `[t][f][a1 …]` (`t = _undefined` when `n == 0`), `sp--` (when `n > 0`),
   then `f.self.vmCall(vm, max(n-1, 0))`. Any other target — non-object,
   non-callable, generator/async/class/method impl types — falls through
   to `nativeFuncObject.vmCall`, i.e. today's exact path and error text.
2. **`funcProtoApplyObject`**: same rewrite for `[f][apply][t][arr]` →
   `[t][f][elems …]`. Elements are copied straight from a dense
   `arrayObject`'s backing slice (nil → `_undefined`) onto the expanded
   stack; any other array-like goes through `createListFromArrayLike` as
   now and is then copied. `undefined`/`null` `arr` → zero args; `n < 2`
   handled as today. Then `f.self.vmCall(vm, len)`. Same target
   eligibility rule and fallback as `call`.
3. **`boundFuncObject.vmCall`** (type exists; currently inherits the
   native path whose closure does `append(args, call.Arguments...)` per
   call). `boundFuncObject` gains `boundThis Value` and `boundArgs []Value`
   fields set by `functionproto_bind`. `vmCall(vm, n)`: if `wrapped.self`
   is an eligible target (same rule) → `stack.expand`, shift the live args
   up by `len(boundArgs)`, write `[boundThis][wrapped][boundArgs …, a …]`,
   then `wrapped.self.vmCall(vm, len(boundArgs)+n)`; otherwise fall through
   to the native path. `new bound()` keeps its current construct path.
   `Function.prototype.call.bind(Object.prototype.hasOwnProperty)` thus
   becomes two in-place rewrites and one native call: zero allocations.

### Recursion and overflow

The rewrite pushes no ctx; the target's `vmCall` pushes exactly one, as a
direct call does, so `maxCallStackSize` accounting is unchanged (today a
`.call` costs two ctxs).

### Observable change

The intrinsic's own frame (`call`, `apply`, the bound wrapper) no longer
appears in `Error.stack` captured inside the target — matching V8 and
matching what a direct call already shows. The characterization suite pins
the new trace shape (§7). Everything else — this-binding, argument counts,
`arguments`, `new.target`, strictness, exceptions thrown by the target,
`apply` with holes, array-likes, non-arrays — is asserted identical to
master. The same applies to the TypeError thrown by apply for a non-object
argument list: its message is unchanged but the intrinsic's frame no longer
appears in its stack.

## 5. Change C — micro allocations

Four independent, mechanical fixes; signatures and behaviour unchanged.

1. **`toPropertyKey`** (`runtime.go`): if `key` is already a `String` or a
   `*Symbol`, return `key` itself; otherwise `key.ToString()` as now. All
   28 callers benefit.
2. **`_defineOwnProperty`** (`object.go`): in the new-property branch
   (`existingValue == nil`), perform the extensibility check, then the
   "all-true data descriptor → return `descr.Value, true`" shortcut,
   and only then allocate `&valueProperty{}`. The existing-property branch
   is unchanged.
3. **`assertCallable` method-value caching** (`func.go`): a
   `callFn func(FunctionCall) Value` field on `baseJsFuncObject`, filled
   on first `assertCallable` and returned thereafter; the same for the
   arrow, async, async-arrow and class impl types that return `f.Call`.
   `nativeFuncObject` already returns its stored `f.f`. Pointer equality
   of the returned func is not promised and not tested.
4. **`intToValue` for indices** (`builtin_array.go`, plus the handful of
   sites in `array.go`/`value.go` where an index is passed to JS):
   replace direct `valueInt(k)` boxing with `intToValue(k)`. Identical
   values; hits the batch-1 pointer-free cache for `[-1024, 65536)`.

## 6. Change E — `NewObjectWithCapacity`

```go
// NewObjectWithCapacity creates an ordinary object (as NewObject) whose
// property storage is pre-sized for n string-keyed properties, so that
// defining up to n properties causes no map or key-list growth.
// n <= 0 behaves exactly like NewObject.
func (r *Runtime) NewObjectWithCapacity(n int) *Object
```

Allocates `values = make(map[unistring.String]Value, n)` and
`propNames = make([]unistring.String, 0, n)` eagerly — the caller has
declared intent, so batch 3's lazy-map rule does not apply. The object is
otherwise a plain `baseObject` with `Object.prototype`. Overshooting `n`
degrades to ordinary growth; undershooting wastes only the hint.

## 7. Equivalence method and tests

Characterization goldens are captured from unmodified `master` before any
change and asserted identical on the branch (batches 2–3 method).

**Descriptor objects.** `Object.getOwnPropertyDescriptor` /
`getOwnPropertyDescriptors` / `Reflect.getOwnPropertyDescriptor` for
data, accessor, get-only, set-only, non-enumerable, non-configurable,
array-index, `length`, symbol-keyed, Proxy-trapped and absent properties,
checked via `Object.keys`, `JSON.stringify`, `hasOwnProperty`, `in`,
`Object.getOwnPropertyNames`, `Object.entries`, `for…in`,
`Object.prototype.toString`, `Object.getPrototypeOf`,
`Object.isExtensible`; then mutation sequences (assign each key, assign a
new key, `delete` each key, `defineProperty` on each key, `Object.freeze`,
`Object.seal`, `preventExtensions`, `setPrototypeOf`) with key order
re-checked after each step; Go-side `Export()`, `Keys()`, `AppendKeys()`,
`GetOwnDataValue()` on un-materialized and materialized forms.

**`call` / `apply` / bound.** This-binding (sloppy vs strict target,
primitives, `null`/`undefined`), argument counts 0..3, `arguments.length`,
`new.target`; `apply` with dense, sparse and holey arrays, array-likes,
`arguments`, non-arrays (TypeError text), `undefined`/`null`; bound with
0..2 bound args, bound-of-bound, bound `call` (the `has` shape), `new` on a
bound function; exceptions thrown by the target propagate with identical
message; `Error.stack` — the intrinsic-frame removal is the one expected
diff, recorded in the golden as the new shape.

**C items.** `toPropertyKey` with string, symbol, number and
object-with-`toString` keys; `defineProperty` all-true vs partial
descriptors on new and existing properties; `assertCallable` reuse across
calls (behaviour only); array builtins with indices above 255 and above
65536.

**Allocation acceptance** (`testing.AllocsPerRun`, zero slack):

| Operation | Allocs |
|-----------|--------|
| descriptor creation | 2 (`Object` + `descriptorObject`) |
| `f.call(t, a)`, `bound(a)` | 0 beyond a direct call |
| `arr.find` / `map` / `forEach` per element | 0 |
| `toCallable` on a JS function | 0 after the first |
| `Object.defineProperty` all-true on a new key | map insert only |
| `NewObjectWithCapacity(n)` + n `DefineDataProperty` (ASCII keys) | defines add no growth allocations: exactly 1 (the small map's first group, allocated by Go on first insert) for n ≤ 8 and 0 for n > 8 |

**Suites.** `go test ./...`, `-race`, `GOARCH=386`, test262
(`testdata/test262`) — descriptor objects and
`Function.prototype.call/apply/bind` are among its most heavily exercised
areas.

**Compatibility.** No public API removed or changed; one addition
(`NewObjectWithCapacity`). `Error.stack` from inside a `call`/`apply`/bound
target loses the intrinsic's own frame. Everything else is intended to be
unobservable.

## 8. App-side patch (for the user to apply; not part of this branch)

In `rcx-go-crud` `internal/v8engine`, at the four sites that create an
object and then define a known field set — `goja_bridge.go:120`
(`len(next.fields)`), `goja_bridge.go:161`, `goja_owned.go:521`,
`goja_context.go:235` — replace `rt.NewObject()` with
`rt.NewObjectWithCapacity(len(<field set about to be defined>))`.
`goja_intl.go` and `goja_session.go:363` are one-off setup objects and
stay as they are.

The batch-2 §8 patch (`ownDataValue` → `GetOwnDataValue`) should be
applied before qualifying this batch so that the descriptor gain is
measured on the remaining JS-side descriptors.

## 9. Risks

1. **`descriptorObject` invariants** — an `objectImpl` override that
   forgets a state (un-materialized vs materialized) would surface as a
   wrong key order or a lost write. Mitigated by mirroring the batch-3
   intrinsics pattern, the mutation-sequence goldens, and test262's
   descriptor coverage.
2. **`vmCall` rewrite arithmetic** — an off-by-one in the stack window
   corrupts the caller's frame. Mitigated by the argument-count matrix
   (0..3 args, 0..2 bound args, `apply` with 0..3 elements) and by
   `-race`/386 runs; the eligibility rule falls back to today's path for
   every impl type not explicitly listed.
3. **Bound-function field addition** — `functionproto_bind` must populate
   `boundThis`/`boundArgs` consistently with the closure it still builds
   for the Go-facing path; a divergence would show as a `call` vs
   `Call` behavioural difference, which the goldens compare directly.
4. **Stack-trace shape** — the one accepted observable change; documented
   here and pinned by the golden.

## 10. Sequencing

C (independent, mechanical) → E (small API) → A (descriptor object) → B1
(`call`, then `apply`, then bound). Each change lands with its goldens,
allocation assertions and a per-change benchmark in the commit message;
the branch is qualified as a whole on the GAP harness against `50ffab1`.

## 11. Expected qualification signals

After the batch, in the CA/US profiles: `valuePropToDescriptorObject`
bytes ≈ 40% of before (on the JS-side share that remains after the §8
patch); `setOwnStr` and `mapassign_faststr` CPU down; `functionproto_call`
and `boundCallable.func1` absent from the CPU profile; `asciiString.ToString`
and `_defineOwnProperty` absent from the alloc profile; `defineOwnPropertyStr`
flat bytes roughly halved once the §8 E patch is applied.
