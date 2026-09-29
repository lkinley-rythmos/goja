package goja

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestBlockScopeDirectEval runs testdata/v8_block_eval.json: a switch that
// declares a let, const or class and makes a direct eval call (at top
// level, in a function, through new Function and eval, in a later case, in
// default:), then 400 generated scripts that nest switches, blocks, loops,
// catch clauses, class static blocks and functions declaring bindings and
// calling eval. Each must finish (a switch's block size wrapped to 2^32-1
// with a direct eval, and the run grew the stack until the process was
// killed) and give Node.js 18.20.3's result, compiled as a vm.Script and
// run in a new context.
func TestBlockScopeDirectEval(t *testing.T) {
	data, err := os.ReadFile("testdata/v8_block_eval.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases [][2]string
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		vm := New()
		timer := time.AfterFunc(5*time.Second, func() { vm.Interrupt("hang") })
		got := runForNode(vm, c[0])
		timer.Stop()
		if got != c[1] && !sameDuplicateError(got, c[1]) {
			t.Errorf("%s\n got: %s\nwant: %s", c[0], got, c[1])
		}
	}
}

// runForNode runs src and renders its result as the fixtures record
// Node.js's: "ok: " and its value as JSON, or the error's constructor and
// message.
func runForNode(vm *Runtime, src string) string {
	v, err := vm.RunString(src)
	var ex *Exception
	switch {
	case err == nil && IsUndefined(v):
		// JSON.stringify(undefined) is undefined.
		return "ok: undefined"
	case err == nil:
		b, _ := json.Marshal(v.Export())
		return "ok: " + string(b)
	case errors.As(err, &ex):
		o, ok := ex.Value().(*Object)
		if !ok {
			return "thrown: " + ex.Value().String()
		}
		name, msg := o.Get("constructor").(*Object).Get("name").String(), o.Get("message").String()
		if name == "SyntaxError" {
			msg = syntaxErrorMessage(errors.New(strings.TrimPrefix(msg, "SyntaxError: ")))
		}
		return name + ": " + msg
	}
	return err.Error()
}

var duplicateError = regexp.MustCompile(`^SyntaxError: Identifier '[^']*' has already been declared$`)

// sameDuplicateError: with more than one redeclaration in a script, goja
// may report another of them than V8.
func sameDuplicateError(got, want string) bool {
	return duplicateError.MatchString(got) && duplicateError.MatchString(want)
}

// TestValueStackBounded: no instruction can grow the value stack past
// maxValueStackSlots, nor make a stash or a block of that size, on 32-bit
// platforms too; it throws V8's RangeError instead of allocating.
func TestValueStackBounded(t *testing.T) {
	rangeErr := rangeError("Maximum call stack size exceeded")
	for _, tc := range []struct {
		name string
		f    func()
	}{
		{"expand", func() { var s valueStack; s.expand(maxValueStackSlots) }},
		{"a wrapped size", func() { slotCount(1<<32 - 1) }},
		{"the bound", func() { slotCount(maxValueStackSlots) }},
		// A block of the wrapped size, entered: on 32-bit platforms its
		// size converted to -1.
		{"enterBlock", func() {
			vm := New().vm
			vm.sp = 0
			(&enterBlock{stackSize: 1<<32 - 1}).exec(vm)
		}},
		{"enterBlock's stash", func() {
			vm := New().vm
			(&enterBlock{stashSize: 1<<32 - 1}).exec(vm)
		}},
	} {
		func() {
			defer func() {
				if x := recover(); x != rangeErr {
					t.Errorf("%s: recovered %v", tc.name, x)
				}
			}()
			tc.f()
			t.Errorf("%s: no RangeError", tc.name)
		}()
	}
	if n := slotCount(maxValueStackSlots - 1); n != maxValueStackSlots-1 {
		t.Fatalf("slotCount(%d) = %d", maxValueStackSlots-1, n)
	}
}
