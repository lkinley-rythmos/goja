package goja

import (
	"fmt"
	"math"
	"reflect"
	"strings"
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
// Measured on Go 1.25 (swiss maps): n=8 empty=4 filled=5 plain=8; n=16
// empty=7 filled=7 plain=14; n=64 empty=7 filled=7 plain=20 — re-measure on
// a toolchain bump.
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

// TestNewHostObjectWithCapacity: the object is an ordinary one, as
// NewObject's, with its storage reserved as NewObjectWithCapacity reserves
// it.
func TestNewHostObjectWithCapacity(t *testing.T) {
	vm := New()
	for _, n := range []int{math.MinInt, -1, 0} {
		o := vm.NewHostObjectWithCapacity(n).self.(*baseObject)
		if o.values != nil || o.propNames != nil {
			t.Fatalf("n=%d: storage must stay lazy as in NewObject", n)
		}
	}
	obj := vm.NewHostObjectWithCapacity(3)
	o := obj.self.(*baseObject)
	if o.values == nil || len(o.propNames) != 0 || cap(o.propNames) != 3 {
		t.Fatalf("n=3: values=%v propNames len=%d cap=%d", o.values != nil, len(o.propNames), cap(o.propNames))
	}
	if obj.ClassName() != classObject || obj.Prototype() != vm.global.ObjectPrototype || !obj.self.isExtensible() {
		t.Fatal("NewHostObjectWithCapacity must produce an ordinary extensible Object")
	}
	for i, k := range []string{"c", "a", "b"} {
		if err := obj.Set(k, i); err != nil {
			t.Fatal(err)
		}
	}
	if keys := obj.Keys(); !reflect.DeepEqual(keys, []string{"c", "a", "b"}) {
		t.Fatalf("keys %v", keys)
	}
	for i := 0; i < 20; i++ {
		if err := obj.Set(fmt.Sprintf("k%d", i), i); err != nil {
			t.Fatal(err)
		}
	}
	if keys := obj.Keys(); len(keys) != 23 || keys[0] != "c" || keys[22] != "k19" {
		t.Fatalf("keys after growth: %v", keys)
	}
	// It allocates as NewObjectWithCapacity does, empty and filled.
	descr := PropertyDescriptor{Value: valueTrue, Writable: FLAG_TRUE, Enumerable: FLAG_TRUE, Configurable: FLAG_TRUE}
	for _, n := range []int{4, 16} {
		keys := make([]unistring.String, n)
		for i := range keys {
			keys[i] = unistring.String(fmt.Sprintf("k%d", i))
		}
		fill := func(o *Object) {
			for _, k := range keys {
				o.self.defineOwnPropertyStr(k, descr, true)
			}
		}
		host := testing.AllocsPerRun(50, func() { fill(vm.NewHostObjectWithCapacity(n)) })
		sized := testing.AllocsPerRun(50, func() { fill(vm.NewObjectWithCapacity(n)) })
		if host != sized {
			t.Errorf("n=%d: NewHostObjectWithCapacity and %d defines allocated %v times, NewObjectWithCapacity %v", n, n, host, sized)
		}
	}
	// From a script, as an object NewObject makes.
	for _, mk := range []func() *Object{vm.NewObject, func() *Object { return vm.NewHostObjectWithCapacity(4) }} {
		if err := vm.Set("o", mk()); err != nil {
			t.Fatal(err)
		}
		v, err := vm.RunString(`o.b = 1; o.a = 2; o[1] = 3; Object.getPrototypeOf(o) === Object.prototype && Object.isExtensible(o) && Reflect.ownKeys(o).join() + Object.prototype.toString.call(o) + JSON.stringify(o)`)
		if err != nil {
			t.Fatal(err)
		}
		if want := `1,b,a[object Object]{"1":3,"b":1,"a":2}`; v.String() != want {
			t.Fatalf("got %s, want %s", v, want)
		}
	}
}

// TestNewHostObjectWithCapacityCharge: the allocation counter charges the
// object and n insertions into it what it charges NewObject and the same
// insertions, the header only, whatever n; NewObjectWithCapacity charges
// the reserved storage besides.
func TestNewHostObjectWithCapacityCharge(t *testing.T) {
	charge := func(n int, mk func(r *Runtime) *Object) int64 {
		r := New()
		r.SetAllocationLimit(0)
		o := mk(r)
		for i := 0; i < n; i++ {
			if err := o.DefineDataProperty(fmt.Sprintf("k%d", i), intToValue(int64(i)), FLAG_TRUE, FLAG_TRUE, FLAG_TRUE); err != nil {
				t.Fatal(err)
			}
		}
		return r.AllocatedBytes()
	}
	for _, n := range []int{-1, 0, 1, 8, 9, 100} {
		plain := charge(n, (*Runtime).NewObject)
		host := charge(n, func(r *Runtime) *Object { return r.NewHostObjectWithCapacity(n) })
		sized := charge(n, func(r *Runtime) *Object { return r.NewObjectWithCapacity(n) })
		if host != plain || plain != allocSizeBaseObject {
			t.Errorf("n=%d: NewHostObjectWithCapacity charged %d, NewObject %d, want %d", n, host, plain, allocSizeBaseObject)
		}
		if want := plain + int64(max(n, 0))*allocSizePropSlot; sized != want {
			t.Errorf("n=%d: NewObjectWithCapacity charged %d, want %d", n, sized, want)
		}
	}
}

// TestNewHostObjectWithCapacityHuge: a capacity too large to reserve
// fails as it does for NewObjectWithCapacity.
func TestNewHostObjectWithCapacityHuge(t *testing.T) {
	vm := New()
	outcome := func(mk func(int) *Object, n int) (s string) {
		defer func() {
			if x := recover(); x != nil {
				s = fmt.Sprint("panic: ", x)
			}
		}()
		o := mk(n).self.(*baseObject)
		return fmt.Sprint(o.values != nil, cap(o.propNames))
	}
	for _, n := range []int{math.MaxInt, 1 << 50} {
		host, sized := outcome(vm.NewHostObjectWithCapacity, n), outcome(vm.NewObjectWithCapacity, n)
		t.Logf("n=%d: %s", n, host)
		if host != sized || !strings.HasPrefix(host, "panic: ") {
			t.Errorf("n=%d: NewHostObjectWithCapacity %s, NewObjectWithCapacity %s", n, host, sized)
		}
	}
}
