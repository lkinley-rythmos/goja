package goja

import "testing"

// TestDestructKeyedSourceReceiver: a read through the source an object
// pattern with a rest element reads the value through, by string, index or
// symbol key, gives the getter the value as this when the receiver is the
// source itself or none, and leaves any other receiver as it is.
// TestObjectRestPatternReceiver makes the same reads from script.
func TestDestructKeyedSourceReceiver(t *testing.T) {
	r := New()
	v, err := r.RunString(`(function () {
		const sym = Symbol("s");
		const src = { get a() { return this; }, get 0() { return this; }, get [sym]() { return this; } };
		return [src, sym];
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	arr := v.(*Object)
	val, sym := arr.Get("0").(*Object), arr.Get("1").(*Symbol)
	other := r.NewObject()
	reads := map[string]func(src *Object, receiver Value) Value{
		"string": func(src *Object, receiver Value) Value { return src.self.getStr("a", receiver) },
		"index":  func(src *Object, receiver Value) Value { return src.self.getIdx(valueInt(0), receiver) },
		"symbol": func(src *Object, receiver Value) Value { return src.self.getSym(sym, receiver) },
	}
	for kind, read := range reads {
		src := r.newDestructKeyedSource(val)
		if got := read(src, src); got != val {
			t.Fatalf("%s key, the source as receiver: this = %v, want the value", kind, got)
		}
		if got := read(src, nil); got != val {
			t.Fatalf("%s key, no receiver: this = %v, want the value", kind, got)
		}
		if got := read(src, other); got != other {
			t.Fatalf("%s key, another receiver: this = %v, want it", kind, got)
		}
	}
}
