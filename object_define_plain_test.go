package goja

import "testing"

// TestDefineOwnProperty_PlainOverPlain: defining a plain data property
// (writable, enumerable, configurable, with a value) over one that is
// already plain stores the new value, keeps the key's position, and
// allocates nothing; the property stays plain.
func TestDefineOwnProperty_PlainOverPlain(t *testing.T) {
	r := New()
	v, err := r.RunString(`({a: 1, b: 2, c: 3})`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*Object)
	b, ok := o.self.(*baseObject)
	if !ok {
		t.Fatalf("fixture is %T", o.self)
	}
	values := []Value{valueInt(7), valueInt(8)}
	i := 0
	if allocs := testing.AllocsPerRun(100, func() {
		b.defineOwnPropertyStr("b", PropertyDescriptor{Value: values[i%2], Writable: FLAG_TRUE, Enumerable: FLAG_TRUE, Configurable: FLAG_TRUE}, true)
		i++
	}); allocs != 0 {
		t.Fatalf("%v allocs", allocs)
	}
	got, err := r.RunString(`(function (o) {
		const d = Object.getOwnPropertyDescriptor(o, "b");
		return Object.keys(o).join() + "|" + [d.value, d.writable, d.enumerable, d.configurable].join();
	})`)
	if err != nil {
		t.Fatal(err)
	}
	fn, _ := AssertFunction(got)
	res, err := fn(nil, o)
	if err != nil {
		t.Fatal(err)
	}
	if want := "a,b,c|" + values[(i-1)%2].String() + ",true,true,true"; res.String() != want {
		t.Fatalf("%s, want %s", res, want)
	}
	if _, bare := b.values["b"].(*valueProperty); bare {
		t.Fatal("b is stored as a valueProperty")
	}
}

// TestDefineOwnProperty_PlainOverOther: a plain definition over a
// property that is not plain goes through validation as before: it
// makes a configurable read-only, hidden or accessor property plain, and
// is rejected on a non-configurable one.
func TestDefineOwnProperty_PlainOverOther(t *testing.T) {
	const script = `(function () {
		const out = [];
		const plain = {value: 9, writable: true, enumerable: true, configurable: true};
		const o = {};
		Object.defineProperty(o, "ro", {value: 1, writable: false, enumerable: true, configurable: true});
		Object.defineProperty(o, "hid", {value: 1, writable: true, enumerable: false, configurable: true});
		Object.defineProperty(o, "acc", {get() { return 1; }, enumerable: true, configurable: true});
		Object.defineProperty(o, "fixed", {value: 1, writable: true, enumerable: true, configurable: false});
		o.p = 1;
		for (const k of ["ro", "hid", "acc", "fixed", "p", "new"]) {
			let r;
			try { Object.defineProperty(o, k, plain); r = "ok"; } catch (e) { r = e.name; }
			const d = Object.getOwnPropertyDescriptor(o, k);
			out.push(k + ":" + r + ":" + [d.value, d.writable, d.enumerable, d.configurable].join("/"));
		}
		const f = Object.freeze({x: 1});
		try { Object.defineProperty(f, "x", plain); out.push("frozen:ok"); } catch (e) { out.push("frozen:" + e.name); }
		out.push(Reflect.defineProperty(Object.freeze({y: 1}), "y", plain));
		return out.join(" ");
	})()`
	v, err := New().RunString(script)
	if err != nil {
		t.Fatal(err)
	}
	want := "ro:ok:9/true/true/true hid:ok:9/true/true/true acc:ok:9/true/true/true fixed:TypeError:1/true/true/false p:ok:9/true/true/true new:ok:9/true/true/true frozen:TypeError false"
	if v.String() != want {
		t.Fatalf("got  %s\nwant %s", v, want)
	}
}
