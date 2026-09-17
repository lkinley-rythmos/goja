# Goja batch 5 — one allocation per object, pre-sized object literals, no re-boxed string coercions

**Date:** 2026-09-17
**Status:** approved design, not yet implemented
**Branch:** `perf/allocation-shape-batch5` (from `master` at `3ebdf77`)

## 1. Motivation and evidence

Batch 4 (`docs/superpowers/specs/2026-09-16-call-path-descriptors-batch4-design.md`)
was qualified on the GAP Get Profile workload in `rcx-go-crud`
(`docs/GOJA-GAP-FORK-BATCH4-RESULTS.md` there, with both app patches):
US mean 47.27 → 42.05 ms, p95 59.62 → 53.10 ms, CPU/request 43.05 → 37.85 ms.
The post-batch-4 CA profile (`.qualification/goja-gap/batch4-profile-candidate`,
40 requests, 1125 MB allocated, 1.47 s CPU sampled) shows the engine's
allocation volume is now dominated by *object creation itself* rather than
by any one builtin:

| Site | MB | % bytes | What it is |
|------|----|---------|------------|
| `newArray` | 96.5 | 8.6 | every array: `&Object{}` + `&arrayObject{}` + values slice |
| `newFunc` + `newArrowFunc` | 62 | 5.5 | every closure: `&Object{}` + func impl |
| `_put` (via `_putProp`) | 49 | 4.4 | object-literal initializers: `newObject` then `putProp` per key, map and key-list growth |
| `newDescriptorObject` | 42 | 3.7 | remaining JS-side descriptors: `&Object{}` + `&descriptorObject{}` |
| `newBaseObjectObj` | 26 | 2.3 | plain objects: `&Object{}` + `&baseObject{}` |
| `createArrayIterator` | 26.5 | 2.4 | `for…of` over arrays: `&Object{}` + `&arrayIterObject{}` |
| `stringproto_indexOf` → `asciiString.toString` | 10 | 0.9 | `toString()` on a value that already is a `String` re-boxes it |

CPU: `mallocgc` 12%, `maps.(*table).grow` 2.7%, and ~13% in string hashing
and map access for property lookups (not addressed here — see §8).

Every goja object is two heap objects: the public `*Object` (16 B: `self`,
`runtime`) and its `objectImpl`. Fusing them for the hot constructors
removes one malloc and one GC object per array, plain object, closure,
descriptor and iterator. Object literals grow their map and key list from
zero although the compiler knows the property count. `Value.toString()`
on a value that already is a `String` allocates an interface box.

## 2. Scope

- **A.** One allocation per object for the hot constructors (box structs).
- **B.** Pre-sized object literals (`newObject(n)` opcode; `newObjectSized`).
- **C.** `toStringValue` helper and a sweep of the string/regexp builtins'
  coercion sites.

Deferred, on record (§8): map-free small objects; per-callsite global
lookup caching; runtime snapshot for the bootstrap prelude.

## 3. Change A — one allocation per object

### Mechanism

A box type per hot impl, impl first so `&box.impl == &box`:

```go
type baseObjectBox struct {
	baseObject
	obj Object
}
type arrayObjectBox struct {
	arrayObject
	obj Object
}
type funcObjectBox struct {
	funcObject
	obj Object
}
type arrowFuncObjectBox struct {
	arrowFuncObject
	obj Object
}
type methodFuncObjectBox struct {
	methodFuncObject
	obj Object
}
type descriptorObjectBox struct {
	descriptorObject
	obj Object
}
type arrayIterObjectBox struct {
	arrayIterObject
	obj Object
}
```

Each changed constructor allocates one box, sets `b.obj.runtime = r`,
wires `impl.val = &b.obj` and `b.obj.self = &b.impl`, then runs the
existing initialisation unchanged.

### Constructors changed

- `newBaseObject(proto, class)` (`runtime.go`) — used by `NewObject`,
  `NewObjectWithCapacity`/`newObjectSized`, `CreateObject`,
  `Object.create`, JSON parsing, and the `newObject` opcode.
  `newBaseObjectObj(obj, proto, class)` keeps its signature and body for
  callers that already own an `Object` (global object, `Reflect`,
  templated materialisation).
- `newArray(proto)` (`builtin_array.go`) — every array (literals, `map`/
  `filter`/`slice` results, `Array.from`, `NewArray`).
- `newFunc`, `newMethod`, `newArrowFunc` (`runtime.go`):
  `initBaseJsFunction` and `initArrowFunc` take the `*Object` as a
  parameter instead of allocating it; every other caller (`newAsyncFunc`,
  `newClassFunc`, generator and async variants, …) passes
  `&Object{runtime: r}` and is otherwise untouched.
- `newDescriptorObject` (`object_descriptor.go`).
- `createArrayIterator` (`array.go`).

### Invariants

- Nothing copies `Object` by value (`grep '= Object{'` over the tree is
  empty), so `*Object` identity is stable and unaffected.
- Go's GC keeps a whole allocation alive through an interior pointer: an
  `Object` reachable only through `self`, or an impl reachable only
  through `val`, is never collected early. No `unsafe`.
- The representation-swap sites — `array.go` dense→sparse,
  `array_sparse.go` sparse→dense, `object_template.go` materialisation,
  `object.go` guarded→base, `func.go` generator — assign a freshly
  allocated impl into `val.self`. The box's original impl then becomes
  ≤ ~120 B of dead weight held by the `Object` until it dies: rare and
  bounded. Templated objects, generators, proxies, typed arrays, maps and
  sets are deliberately not boxed.

### Effect per object

Allocations 2 → 1; one fewer GC object; bytes ≈ −0…−16 depending on the
size class (one allocation header saved, 16 B `Object` added). `{}` today:
`Object` (16 B) + `baseObject` (~136 B) → one ~152 B box.

Not changed: `objectImpl`, `Object`'s fields, any public API,
`newBaseObjectObj`, every non-hot constructor.

## 4. Change B — pre-sized object literals

### Compiler

`compiledObjectLiteral.emitGetter` (`compiler_expr.go`) counts the
properties that each add one own string key before it emits anything:
`*ast.PropertyKeyed` of kind value/method/get/set with a non-computed key
that is not `__proto__`, plus `*ast.PropertyShort`. Not counted: computed
keys (unknown until runtime; may collide or be symbols), `__proto__: …`
(sets the prototype), `...spread` (unknown count). A getter+setter pair
on one key counts twice — a harmless over-estimate of one slot. It emits
`newObject(n)`; the opcode type becomes `type newObject uint32` (today a
struct singleton `_newObject`), and every `emit(newObject)` site becomes
`emit(newObject(n))`.

### VM and runtime

```go
func (n newObject) exec(vm *vm) {
	vm.push(vm.r.newObjectSized(int(n)))
	vm.pc++
}
```

`newObjectSized(n int) *Object` (`runtime.go`) is the body
`NewObjectWithCapacity` has today (`n <= 0` → lazy storage exactly as
`NewObject`; otherwise `values = make(map, n)`,
`propNames = make([]unistring.String, 0, n)`); `NewObjectWithCapacity`
calls it. One implementation, and `newBaseObject` (Change A) underneath.

### Writes

`putProp` is untouched: it already goes `_putProp → _put` (no prototype
walk), one map insert plus a `propNames` append that now stays within
capacity. Method/getter/setter definitions go through
`defineOwnPropertyStr` and benefit from the pre-sized map the same way.

### Effect

For k ≤ 8 keys: map header + one group (allocated on the first insert, per
the batch-4 finding) instead of header + group + regrowths; for k > 8 the
table is sized once. `propNames`: one allocation instead of
⌈log₂ k⌉ growth steps. Zero-property literals change nothing.

Compatibility: enumeration order, semantics and errors are unchanged;
only initial capacity differs. goja has no bytecode serialisation, so the
opcode change has no persistence concern.

## 5. Change C — `toStringValue`

Root cause: `Value.toString() String` on an `asciiString`,
`unicodeString` or `*importedString` returns the receiver, but returning
a concrete type through the `String` interface allocates a 16 B box per
call. `stringproto_indexOf` does it twice per call.

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

lives next to `toPropertyKey` in `runtime.go`. A mechanical sweep replaces
`X.toString()` with `toStringValue(X)` at every site in `builtin_string.go`
(49 occurrences) and `builtin_regexp.go` (25) where `X`'s static type is
`Value` (`call.This`, `call.Argument(n)`, `Value` locals). Sites whose
receiver has a concrete non-`String` static type are left alone.
Semantics are identical: `*Object` receivers still invoke JS
`toString`/`valueOf`; `null`/`undefined` still coerce to their strings;
Symbols still throw the same TypeError.

## 6. Equivalence method and tests

Characterization goldens are captured from unmodified `master` at
`3ebdf77` before any change and asserted identical on the branch
(batches 2–4 method).

**Object literals.** Key order and values for literals mixing keyed
values, shorthand, computed keys, methods, getter/setter pairs on the
same key, `__proto__`, spread (before/after/between static keys),
duplicate keys, more than 8 keys, numeric-string keys, nested literals —
observed through `Object.keys`, `JSON.stringify`,
`Object.getOwnPropertyDescriptors`, `for…in`.

**Co-allocated objects.** Behaviour across representation swaps —
dense→sparse (`a[1e6] = 1`) and back (`a.length = 3`, `delete`),
generator objects, arrays as prototypes, `Object.create` chains,
`Export()`/`ExportTo`, `Keys()`, `GetOwnDataValue`, JSON round trips,
`instanceof`, `Object.prototype.toString`. Go-side: `*Object` identity
survives a dense→sparse→dense round trip; for each boxed constructor
`impl.val == obj && obj.self == impl`.

**String coercions.** `indexOf`/`lastIndexOf`/`includes`/`startsWith`/
`endsWith`/`split`/`replace`/`match`/`search`/`test`/`exec` with string,
number, object-with-`toString`, `null`/`undefined` and Symbol arguments
(TypeError text), plus `String.prototype.*.call(nonString)`.

**Allocation acceptance** (`testing.AllocsPerRun`, exact; "before"
measured on `3ebdf77` at plan time):

| Operation | Before → after |
|-----------|----------------|
| `[]` / `[1, 2, 3]` literal | 3 → 2 (box + values slice) |
| `{}` literal | 2 → 1 |
| `{a: 1, b: 2, c: 3}` literal | measured at plan time → box + map header + group + names (exact count pinned) |
| `function () {}` / arrow creation | 2 (+ stash) → 1 (+ stash) |
| `Object.getOwnPropertyDescriptor` | 2 beyond a miss → 1 |
| `for (x of arr)` iterator creation | 2 → 1 |
| `s.indexOf('x')` from JS | 2 → 0 |
| `NewObject()` from Go | 2 → 1 |

**Suites.** `go test ./...`, `-race`, `GOARCH=386` (box structs change
layout, so the 386 leg matters), test262.

**Compatibility.** No public API change; no observable behaviour change
intended. Memory: +16 B per boxed impl, −1 allocation header; a
representation swap leaves a dead impl inside the box (rare, bounded).

## 7. Risks

1. **A stale `val`/`self` wiring in a box constructor** would surface as
   an object whose `self.val` is not itself — caught by the Go-side wiring
   test on every boxed constructor and by the goldens.
2. **A compiler miscount** in Change B only affects initial capacity;
   under-counting degrades to today's growth, over-counting wastes a
   slot. The literal goldens pin order and content.
3. **A `toStringValue` site whose receiver could be a `*Object`** is not
   a risk: the helper falls through to `toString()` for non-`String`
   values, so JS `toString`/`valueOf` still run.
4. **Layout sensitivity** — the 386 leg and `-race` runs cover the box
   structs.

## 8. Deferred

- Map-free representation for small objects (≤ 8 properties): attacks
  the ~13% CPU in string hashing/map access and most object bytes; a
  batch of its own with heavy characterisation.
- Per-callsite global lookup caching (`templatedObject.getStr` 4.8% CPU).
- Runtime snapshot/clone for the per-request bootstrap prelude (10%
  CPU); app-side trimming of the prelude is the cheaper route.
- App side, outside goja: `bufio.NewReaderSize` under
  `mongo.Cursor.Decode` (106 MB, 9.4% of all bytes); host projection
  (`snapshotGojaHostMode` + `captureMode`, 12%).

## 9. Sequencing

C (mechanical sweep) → B (compiler + opcode + `newObjectSized`) → A
(boxes, one constructor family per task: plain objects, arrays,
functions, descriptor + array iterator). Each lands with its goldens,
allocation assertions and a per-change benchmark in the commit message;
the branch is qualified as a whole on the GAP harness against `3ebdf77`.

## 10. Expected qualification signals

`newArray`, `newBaseObjectObj`, `newFunc`, `newArrowFunc`,
`newDescriptorObject`, `createArrayIterator` each lose one allocation per
call (object count halved on those lines); `_put` loses most of its
49 MB; `asciiString.toString` disappears from the allocation profile;
`mallocgc` and `maps.(*table).grow` CPU down.
