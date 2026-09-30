package parser

import (
	"reflect"
	"strings"
	"testing"
)

func TestTransformRegExpIterations(t *testing.T) {
	// A quantified group that may iterate more than once and has capturing
	// groups gets a hidden group at the start of its body, which is a group
	// of its own; the groups after it are numbered on, and backreferences
	// with them.
	for _, tc := range []struct {
		pattern, goText, r2Text string
		iter                    *Iterations
	}{
		{`(?:(a)|b)+`, `(?:()(?:(a)|b))+`, `(?:()(?:(a)|b))+`, &Iterations{
			Groups: []IterationGroup{{Hidden: 1, Inner: []IterationCapture{{Group: 2}}}}, Hidden: []int{1}}},
		{`((a)|b)+(c)\3\1`, ``, `(()(?:(a)|b))+(c)\4\1`, &Iterations{
			Groups: []IterationGroup{{Hidden: 2, Inner: []IterationCapture{{Group: 3}}}}, Hidden: []int{2}}},
		{`(?:a(b?)|c)+`, `(?:()(?:a(b?)|c))+`, `(?:()(?:a(b?)|c))+`, &Iterations{
			Groups: []IterationGroup{{Hidden: 1, Inner: []IterationCapture{{Group: 2, ConsumedBefore: true}}}}, Hidden: []int{1}}},
		{`(?:x?(?:(a)|b)*y|(?=(z)))+`, ``, `(?:()(?:x?(?:()(?:(a)|b))*y|(?=(z))))+`, &Iterations{
			Groups: []IterationGroup{
				{Hidden: 2, Inner: []IterationCapture{{Group: 3}}},
				{Hidden: 1, Inner: []IterationCapture{{Group: 3}}},
			}, Hidden: []int{1, 2}}},
		{`(?<n>a|(b))*\k<n>`, ``, `(?<n>()(?:a|(b)))*\k<n>`, &Iterations{
			Groups: []IterationGroup{{Hidden: 2, Inner: []IterationCapture{{Group: 3}}}}, Hidden: []int{2}}},
		// Without alternatives the body needs no group of its own.
		{`(?:x(a)?)+`, `(?:()x(a)?)+`, `(?:()x(a)?)+`, &Iterations{
			Groups: []IterationGroup{{Hidden: 1, Inner: []IterationCapture{{Group: 2, ConsumedBefore: true}}}}, Hidden: []int{1}}},
		// None: at most one iteration, no capturing group, one in every
		// iteration, or one in a lookaround.
		{`(?:(a)|b)?(a){1}(?:ab)+(?:x(a))+(?:(a)+ ?){3}(?:(?=(a))b)+`, ``, `(?:(a)|b)?(a){1}(?:ab)+(?:x(a))+(?:(a)+ ?){3}(?:(?=(a))b)+`, nil},
	} {
		goText, r2Text, iter, err := TransformRegExpIter(tc.pattern, false, false, false)
		if _, incompat := err.(RegexpErrorIncompatible); err != nil && !incompat {
			t.Fatalf("%s: %v", tc.pattern, err)
		}
		if goText != tc.goText || r2Text != tc.r2Text {
			t.Errorf("%s: %q, %q", tc.pattern, goText, r2Text)
		}
		if !reflect.DeepEqual(iter, tc.iter) {
			t.Errorf("%s: %+v", tc.pattern, iter)
		}
	}
	// The plan is bounded: a million nested groups are refused, and many
	// captures in deeply nested groups get no hidden groups.
	deep := strings.Repeat("(", 1000000) + strings.Repeat(")+", 1000000)
	if _, _, iter, err := TransformRegExpIter(deep, false, false, false); err == nil || iter != nil {
		t.Errorf("nested: %v %v", iter != nil, err)
	}
	wide := strings.Repeat("(?:", 900) + strings.Repeat("(a)", 2000) + strings.Repeat(")+", 900)
	if _, _, iter, _ := TransformRegExpIter(wide, false, false, false); iter != nil {
		t.Errorf("wide: %d groups", len(iter.Groups))
	}
}
