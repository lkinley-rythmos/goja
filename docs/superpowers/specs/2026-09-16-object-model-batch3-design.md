# Goja batch 3 — lazy property maps, eager regexp2, allocation-free Keys

**Date:** 2026-09-16
**Status:** draft, awaiting review
**Branch:** `perf/object-model-batch3` (from `master` at `2a30572`)

## 1. Motivation and evidence

Batch 2 (`docs/superpowers/specs/2026-09-16-iterator-fast-path-batch2-design.md`)
was qualified on the real GAP Get Profile workload in `rcx-go-crud`
(`docs/GOJA-GAP-FORK-BATCH2-RESULTS.md` there): US mean 66.5 → 58.5 ms,
p95 85.6 → 77.5 ms, CPU/request −13%, allocated bytes −27%.

The post-batch-2 profile (`fork2-profile-fork`, CA, 40 requests, 690 MB
sampled) ranks what remains:

| allocation source | share of bytes | mechanism |
|---|---|---|
| `baseObject._put` from `arrayObject.init` (122 MB) and `baseFuncObject.init` (72 MB), plus their `baseObject.init` maps and the `valueProperty` for `name` | **~36%** | every `[]` allocates a Go map to hold `length`; every closure a map for `length`/`name`. `mapassign_faststr` is 7.8% of CPU, mostly these inserts |
| JS-side `Object.getOwnPropertyDescriptor` (`valuePropToDescriptorObject`) | 8.9% | application JS; handled app-side, out of scope (§2) |
| mongo-driver `bson.NewDocumentReader` | 5.6% | outside goja |
| `Object.Keys` + `enumerableIter`/`objectPropIter`/`stringValueFromRaw` | ~4–5% | iterator objects and a boxed `String` per key on every host-side enumeration |
| `regExpExec` match arrays | 4.7% | not in this batch |
| `createRegexp2` (lazy regexp2 engine per clone) | 2.6% | global/sticky patterns rebuild regexp2 the first time a match starts at `lastIndex > 0` |

This batch removes rows 1, 4 and 6.

## 2. Scope

In scope, as independent commits:

- **A.** `baseObject.values` allocated lazily on first write.
- **B.** Dense arrays stop storing `length` in the property map.
- **C.** Functions stop storing `length`/`name` in the property map.
- **D.** Eager regexp2 construction for global/sticky templates.
- **E.** Fast path in `Object.Keys` and a new `Object.AppendKeys`.

Out of scope: inline small-property storage / hidden classes (the general
fix; batch 4 territory); `GetOwnPropertyNames` fast path (not in the
profile); sparse arrays (rare; keep their map entry, conversions adjusted);
engine-side cheaper descriptor objects for JS `getOwnPropertyDescriptor`
(the user handles those call sites app-side); `regExpExec` match arrays.

## 3. Change A — lazy property map

`baseObject.init()` no longer allocates `values`. Go makes reads, `delete`,
`len` and `range` on a nil map safe; only writes need care:

```go
// ensureValues allocates the property map on first write. Objects that
// never receive an own property — iterators, Dates, RegExps, empty arrays,
// most functions — never pay for one.
func (o *baseObject) ensureValues() {
	if o.values == nil {
		o.values = make(map[unistring.String]Value)
	}
}
```

`ensureValues` is called at every site that assigns into the map — the
complete list from a repository-wide grep of `.values[…] =`:

| file | site |
|---|---|
| object.go | `_put` |
| object.go | `setOwnStr` — the new-property branch and the plain-slot overwrite |
| object.go | `defineOwnPropertyStr` — after `_defineOwnProperty` succeeds |
| object_template.go | `getOwnPropStr` (template materialization), `defineOwnPropertyStr`, `deleteStr` (the nil "white hole" marker), `materialiseProps` |

`_delete`, `deleteStr`, `hasOwnPropertyStr`, `getOwnPropStr`, `stringKeys`,
`iterateStringKeys` and every read-only builtin are untouched. No code
tests `values == nil` today. A forgotten write site panics with Go's
"assignment to entry in nil map", so the invariant is fail-loud.

## 4. Change B — dense array `length`

`arrayObject` only. `lengthProp valueProperty` stays as the storage;
`init` stops calling `_put("length", &a.lengthProp)`.

Already virtual, unchanged: `getOwnPropStr` (returns `a.getLengthProp()`
before the map), `setOwnStr` and `defineOwnPropertyStr` (special-case
`length` via `defineArrayLength`/`setLength`).

New `length` branches, reproducing what the map entry provided:

- `hasOwnPropertyStr("length")` → `true`.
- `deleteStr("length", throw)` → `a.checkDelete("length", &a.lengthProp, throw)`; non-configurable, so `false` (TypeError when `throw`).
- `stringKeys(all, accum)`: after the index loop, `if all { append "length" }`, then `baseObject.stringKeys`. Today `length` is the first `propNames` entry, so the order — indices, `length`, other string keys — is unchanged.
- `arrayPropIter.next`: after the index phase, yield once `propIterItem{name: asciiString("length"), value: &a.lengthProp}` (new `lengthDone` flag), then delegate. The value **must** be the field pointer: `Object.freeze`/`seal` (`object_freeze` in builtin_object.go) mutate the yielded `*valueProperty` in place, which is how `length` becomes non-writable today — the map held `&a.lengthProp`, so pointer identity is preserved. `enumerableIter` filters it for `for-in`/`Object.keys`/`JSON` as before.

Conversions:

- dense → sparse (`array.go`, in `expand`): copies `a.baseObject` by value (map now without `length`) then `sa._put("length", &sa.lengthProp)` as today — unchanged.
- sparse → dense (`array_sparse.go`, in `expand`): replace `a._put("length", &ar.lengthProp)` with `ar._delete("length")` — the two objects share the map by value-copy and the sparse one carries a `length` entry that the dense object must not inherit.

Invariant, stated in code: a dense `arrayObject` never has `"length"` in
`values`/`propNames`.

## 5. Change C — function `length` and `name`

**Storage.** `baseFuncObject` keeps `lenProp valueProperty`, gains
`nameProp valueProperty` (value `stringValueFromRaw(name)`, writable
false, enumerable false, configurable true — what `_putProp("name", …)`
builds today, minus the heap-allocated `valueProperty`) and a flag
`intrinsicsVirtual bool` set by `init`. `init` no longer calls
`_put`/`_putProp`. Both intrinsics are virtual together or materialized
together: one flag, two states.

**Where the checks live.** `baseFuncObject` gains overrides for `getStr`,
`getOwnPropStr`, `hasOwnPropertyStr`, `setOwnStr`, `setForeignStr`,
`defineOwnPropertyStr`, `deleteStr`, `stringKeys`, `iterateStringKeys`.
All 15 function constructors go through `baseFuncObject.init` and every
function type embeds `baseFuncObject`, so method promotion applies these
to native, bound, arrow, method, class, generator and async functions.
`funcObject`'s existing overrides (which add `prototype`) currently
delegate to `f.baseObject.X`; they are re-pointed to `f.baseFuncObject.X`
so the chain is `prototype` → intrinsics → map.

**While virtual (`intrinsicsVirtual == true`):**

- `getOwnPropStr("length"/"name")` → `&f.lenProp` / `&f.nameProp`; `getStr` goes through `getStrWithOwnProp` so accessor state is honoured; `hasOwnPropertyStr` → `true`.
- `defineOwnPropertyStr`: run `f._defineOwnProperty(name, &prop, descr, throw)` against the field. `_defineOwnProperty` mutates an existing `*valueProperty` in place and returns the same pointer for every descriptor except the all-`true` data form. Same pointer returned → stay virtual (this is the SetFunctionName case used for `{ m() {} }`, class methods and `obj.x = function(){}` in vm.go: zero allocation). Different value or failure → materialize (below), then store the result — what `baseObject.defineOwnPropertyStr` would have done.
- `setOwnStr("length"/"name")`: non-writable → `typeErrorResult(throw, "Cannot assign to read only property '%s'", name)`, `false`; still virtual. `setForeignStr` resolves through `getOwnPropStr` as `funcObject`'s does.
- `deleteStr`: both configurable; materialize, then `baseObject.deleteStr` (one code path for deletion semantics; `delete f.name` is rare).
- `stringKeys(all)`: emit `length`, `name` first when `all` (both non-enumerable), then the map. `iterateStringKeys`: yield the two items with the **field pointers** first, then delegate — `Object.freeze` mutates the real storage.

**Materialize** (once, irreversible): `ensureValues()`;
`values["length"] = &f.lenProp`, `values["name"] = &f.nameProp`; insert
`"length"`, `"name"` at positions 0 and 1 of `propNames`; clear the flag.
Today `init` places them at the front before anything else, including
`prototype` (materialized later by `funcObject`), so `propNames` order is
`[length, name, prototype?, …user]` in every state. After materialization
every method delegates to `baseObject` and behaves as before.

**Payoff:** a function drops from Object + funcObject + map + map group +
`valueProperty` (5 allocations) to 2.

## 6. Change D — eager regexp2 for global/sticky templates

In `compileRegexpUncached`, when the Go-regexp engine was selected
(`wrapper != nil`) and `global || sticky`, also call
`compileRegexp2(patternStr, multiline, dotAll, ignoreCase, unicode)` and
store it on the template. `clone()` already shares `regexp2Wrapper.rx`, so
clones inherit the engine and `createRegexp2()`'s existing
`if p.regexp2Wrapper != nil { return }` guard makes it a no-op for these
patterns. A `compileRegexp2` failure here (today a panic inside
`createRegexp2`) becomes a returned error → `SyntaxError`. Non-global
patterns always match from `start == 0` and are untouched. Cost: one extra
compile per `(pattern, flags)` per process, both engines resident for
those templates, bounded by the cache cap.

## 7. Change E — `Keys` fast path and `AppendKeys`

```go
// AppendKeys appends the object's own enumerable string keys, in the same
// order Keys returns them, and returns the extended slice. Passing a slice
// with spare capacity makes the call allocation-free for ordinary objects
// and arrays; other objects (Proxy, Go-backed, templated) fall back to the
// generic enumeration and allocate as Keys does.
func (o *Object) AppendKeys(dst []string) []string

func (o *Object) Keys() []string { return o.AppendKeys(nil) }
```

Type switch on `o.self`:

- `*baseObject`: `ensurePropOrder()`; walk `propNames`, skipping entries whose `values[k]` is nil or a non-enumerable `*valueProperty` (the predicate `enumerableIter` applies); append `k.String()` (no-copy for ASCII, as `item.name.String()` today). Allocate the result on the first key with capacity `len(propNames)`, so an object with no keys returns `nil` exactly as today.
- `*arrayObject`: enumerable indices from the dense `values` slice (`strconv.Itoa`), then the `propNames` walk; `length` never appears.
- everything else: today's iterator loop, appending into `dst`.

No copy-on-write marker is needed: the fast path runs no user code
between reads. This is why it is a slice API, not a callback API.

Effect: `Keys` on a plain object goes from ~3 + n allocations to exactly
1; `AppendKeys` into a reused buffer to 0. `GetOwnPropertyNames` gets no
fast path (not in the profile).

## 8. Equivalence method and tests

Sections 3–5 have no "generic path" to compare against, so their golden
tests are *characterization tests*: written against today's behaviour,
they must pass on the unmodified engine before the change (the
implementer proves this first) and still pass after. Together with test262
(which covers Array/Function own-key order, `freeze`, `defineProperty` on
`length`/`name` extensively) that is the equivalence proof.

- *A:* an object that never gets a property has `values == nil`; `keys`, `hasOwnProperty`, `delete`, `for-in`, `JSON.stringify`, `getOwnPropertyNames`, `freeze`/`isFrozen`, `preventExtensions` on such an object behave as today; templated objects still materialize template props and honour the white-hole marker.
- *B:* golden script over dense arrays — `getOwnPropertyNames`, `keys`, `for-in`, `JSON.stringify`, `freeze` then `isFrozen`/`push` throws, `delete arr.length` (false; strict TypeError), `defineProperty(arr,"length",{value})`/`{writable:false}`, `getOwnPropertyDescriptor(arr,"length")`, `Object.assign`, spread; both conversions (grow past the sparse threshold and back) then the same assertions; internal assertion that no dense array has `"length"` in `propNames`.
- *C:* golden script over declaration, expression, arrow, method, class, static method, bound, native, generator, async functions — `getOwnPropertyNames` before/after a user property, `prototype` touched first vs last; `delete f.name`; `defineProperty(f,"name",{value})` stays virtual (internal assertion) and the all-`true` form materializes at the front; `f.name = "x"` sloppy/strict; `freeze` + `isFrozen` + descriptor flags; `class A { static name(){} }`; `bind` result `name`/`length`; descriptors identical before/after materialization.
- *D:* a `g` pattern has both engines on template and clone; `matchAll`/`exec` loops identical for `g`, `y`, `gy`, unicode, empty-match; a non-global pattern has no regexp2 engine; a Go-regexp-rejected pattern still takes the regexp2-only path.
- *E:* `Keys`/`AppendKeys` equal the generic iterator for plain objects (incl. integer-like keys), arrays with holes and non-enumerable elements, a Proxy with `ownKeys` trap, a Go-reflect object, an empty object (`nil`); `AppendKeys` into a reused buffer → 0 allocs.
- test262 on every commit; `-race` on new tests; `GOARCH=386` on the object-model tests.

Benchmarks (allocs/op; baseline captured before each task's change;
expected figures measured on this codebase): `BenchmarkArrayLiteralCreate`,
`BenchmarkFunctionCreate`, `BenchmarkMethodLiteralCreate`,
`BenchmarkArrayIterObjectCreate` (each drops by 2 allocations per
object), `BenchmarkMatchAllLoop` (regexp2 compile gone after first op),
`BenchmarkObjectKeys` (exactly 1), `BenchmarkAppendKeysReused` (0);
sentinels `CallJS`, `Fib`, `VMAdd`, `ForOfArray` unchanged. The user's
GAP harness is the acceptance gate; merge to `master` after qualification.

## 9. Risks

1. A missed map write site → nil-map panic; fail-loud, caught by the suite.
2. Enumeration-order or descriptor drift in arrays/functions — mitigated by before/after characterization tests and test262.
3. Function types overriding property methods elsewhere — grep found only `funcObject`; the plan repeats the grep.
4. Eager regexp2 doubles first-compile cost for global patterns; bounded by the cache.

## 10. Sequencing

Independent commits: A → B → C → D → E, each with tests and a benchmark
delta in its message.
