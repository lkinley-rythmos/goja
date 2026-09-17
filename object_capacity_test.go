package goja

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/dop251/goja/unistring"
)

func TestNewObjectWithCapacity(t *testing.T) {
	vm := New()
	for _, n := range []int{-1, 0} {
		o := vm.NewObjectWithCapacity(n).self.(*baseObject)
		if o.values != nil || o.propNames != nil {
			t.Fatalf("n=%d: storage must stay lazy as in NewObject", n)
		}
	}
	obj := vm.NewObjectWithCapacity(3)
	o := obj.self.(*baseObject)
	if o.values == nil || len(o.propNames) != 0 || cap(o.propNames) != 3 {
		t.Fatalf("n=3: values=%v propNames len=%d cap=%d", o.values != nil, len(o.propNames), cap(o.propNames))
	}
	if obj.ClassName() != classObject || obj.Prototype() != vm.global.ObjectPrototype || !obj.self.isExtensible() {
		t.Fatal("NewObjectWithCapacity must produce an ordinary extensible Object")
	}
	for i, k := range []string{"c", "a", "b"} {
		if err := obj.Set(k, i); err != nil {
			t.Fatal(err)
		}
	}
	if keys := obj.Keys(); !reflect.DeepEqual(keys, []string{"c", "a", "b"}) {
		t.Fatalf("keys %v", keys)
	}
	// Overshooting the hint degrades to ordinary growth.
	for i := 0; i < 20; i++ {
		if err := obj.Set(fmt.Sprintf("k%d", i), i); err != nil {
			t.Fatal(err)
		}
	}
	if keys := obj.Keys(); len(keys) != 23 || keys[0] != "c" || keys[22] != "k19" {
		t.Fatalf("keys after growth: %v", keys)
	}
	if m := obj.Export().(map[string]interface{}); len(m) != 23 || m["k7"] != int64(7) {
		t.Fatalf("export: %v", m)
	}
}

// TestNewObjectWithCapacityNoGrowth: defining n all-true data properties
// into an object pre-sized for n causes no map or key-list growth. Go
// allocates a small map's (hint <= 8) single group on the first insert
// rather than in make, so that case costs exactly one allocation beyond
// the empty object; a larger hint allocates its table in make and the
// defines add nothing. NewObject() with the same defines always costs
// more.
func TestNewObjectWithCapacityNoGrowth(t *testing.T) {
	vm := New()
	descr := PropertyDescriptor{Value: valueTrue, Writable: FLAG_TRUE, Enumerable: FLAG_TRUE, Configurable: FLAG_TRUE}
	for _, n := range []int{4, 8, 16, 64} {
		keys := make([]unistring.String, n)
		for i := range keys {
			keys[i] = unistring.String(fmt.Sprintf("k%d", i))
		}
		fill := func(o *Object) {
			for _, k := range keys {
				o.self.defineOwnPropertyStr(k, descr, true)
			}
		}
		empty := testing.AllocsPerRun(50, func() { vm.NewObjectWithCapacity(n) })
		filled := testing.AllocsPerRun(50, func() { fill(vm.NewObjectWithCapacity(n)) })
		want := 0.0
		if n <= 8 {
			want = 1
		}
		if extra := filled - empty; extra != want {
			t.Errorf("n=%d: defining %d properties into a pre-sized object allocated %v extra times, want %v", n, n, extra, want)
		}
		plain := testing.AllocsPerRun(50, func() { fill(vm.NewObject()) })
		if plain <= filled {
			t.Errorf("n=%d: pre-sizing saved nothing: NewObject+fill %v, NewObjectWithCapacity+fill %v", n, plain, filled)
		}
	}
}
