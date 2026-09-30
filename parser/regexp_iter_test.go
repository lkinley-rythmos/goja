package parser

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestTransformRegExpIterations(t *testing.T) {
	// A quantified group that may iterate more than once and has capturing
	// groups an iteration may lack is told by its own capture, or else by a
	// hidden group that is its body; the groups after it are numbered on,
	// and backreferences with them. One whose body may match nothing is
	// told too, for the iterations regexp2 takes that ECMAScript refuses.
	loop := func(group, last, min, max, depth, parent int, alternatives, reset bool) IterationLoop {
		return IterationLoop{Group: group, Last: last, Min: min, Max: max, Depth: depth, Parent: parent, Alternatives: alternatives, Reset: reset}
	}
	for _, tc := range []struct {
		pattern, goText, r2Text string
		iter                    *Iterations
		groups                  string // see describeGroups
	}{
		{`(?:(a)|b)+`, `(?:((a)|b))+`, `(?:((a)|b))+`, &Iterations{
			Loops:    []IterationLoop{loop(1, 2, 1, -1, 1, -1, true, true)},
			Captures: []IterationCapture{{Group: 2, Loop: 0, Missing: 1}},
			Hidden:   []int{1}, Drop: true}, "hm0 l0"},
		// A group that captures tells its own iterations.
		{`((a)|b)+(c)\3\1`, ``, `((a)|b)+(c)\3\1`, &Iterations{
			Loops:    []IterationLoop{loop(1, 2, 1, -1, 1, -1, true, true)},
			Captures: []IterationCapture{{Group: 2, Loop: 0, Missing: 1}},
			Drop:     true}, "rm0 l0p1 rb1"},
		{`(?:a(b?)|c)+`, `(?:(a(b?)|c))+`, `(?:(a(b?)|c))+`, &Iterations{
			Loops:    []IterationLoop{loop(1, 2, 1, -1, 1, -1, true, true)},
			Captures: []IterationCapture{{Group: 2, Loop: 0, Missing: 1}},
			Hidden:   []int{1}, Drop: true}, "hm0 c1l0"},
		{`(?:x?(?:(a)|b)*y|(?=(z)))+`, ``, `(?:(x?(?:((a)|b))*y|(?=(z))))+`, &Iterations{
			Loops:    []IterationLoop{loop(1, 4, 1, -1, 1, -1, true, true), loop(2, 3, 0, -1, 2, 0, true, true)},
			Captures: []IterationCapture{{Group: 3, Loop: 1, Missing: 2}, {Group: 4, Loop: 0, Missing: 2}},
			Hidden:   []int{1, 2}, Drop: true}, "hm0 hl0m1 l1 a2l0"},
		{`(?<n>a|(b))*\k<n>`, ``, `(?<n>a|(b))*\k<n>`, &Iterations{
			Loops:    []IterationLoop{loop(1, 2, 0, -1, 1, -1, true, true)},
			Captures: []IterationCapture{{Group: 2, Loop: 0, Missing: 1}},
			Drop:     true}, "rm0 l0p1"},
		{`(?:x(a)?){2,5}`, `(?:(x(a)?)){2,5}`, `(?:(x(a)?)){2,5}`, &Iterations{
			Loops:    []IterationLoop{loop(1, 2, 2, 5, 1, -1, false, true)},
			Captures: []IterationCapture{{Group: 2, Loop: 0, Missing: 2}},
			Hidden:   []int{1}, Drop: true}, "hm0 c1l0"},
		{`(x)?(?:(b)|\1)+a`, ``, `(x)?(?:((b)|\1))+a`, &Iterations{
			Loops:    []IterationLoop{loop(2, 3, 1, -1, 1, -1, true, true)},
			Captures: []IterationCapture{{Group: 3, Loop: 0, Missing: 1}},
			Hidden:   []int{2}, Drop: true}, "r hm0 l0"},
		// A body that may match nothing: regexp2's matches have their
		// refused iterations dropped, Go's are left.
		{`(\B)*`, `(\B)*`, `((?:(?<=[0-9A-Z_a-z])(?=[0-9A-Z_a-z])|(?<![0-9A-Z_a-z])(?![0-9A-Z_a-z])))*`, &Iterations{
			Loops: []IterationLoop{loop(1, 1, 0, -1, 1, -1, false, false)}}, "m0"},
		{`(?:(x*))+`, `(?:((x*)))+`, `(?:((x*)))+`, &Iterations{
			Loops:    []IterationLoop{loop(1, 2, 1, -1, 1, -1, false, false)},
			Captures: []IterationCapture{{Group: 2, Loop: 0}},
			Hidden:   []int{1}, Drop: true}, "hm0 l0"},
		// None: at most one iteration, no capturing group, one in every
		// iteration of a body that always consumes something, or one in a
		// lookaround.
		{`(?:(a)|b)?(a){1}(?:ab)+(?:x(a))+(?:(a)+ ?){3}(?:(?=(a))b)+`, ``, `(?:(a)|b)?(a){1}(?:ab)+(?:x(a))+(?:(a)+ ?){3}(?:(?=(a))b)+`, nil, ""},
	} {
		goText, r2Text, iter, err := TransformRegExpIter(tc.pattern, false, false, false)
		if _, incompat := err.(RegexpErrorIncompatible); err != nil && !incompat {
			t.Fatalf("%s: %v", tc.pattern, err)
		}
		if goText != tc.goText || r2Text != tc.r2Text {
			t.Errorf("%s: %q, %q", tc.pattern, goText, r2Text)
		}
		if iter != nil {
			if g := describeGroups(iter.Groups); g != tc.groups {
				t.Errorf("%s: groups %q", tc.pattern, g)
			}
			iter.Groups = nil
		}
		if !reflect.DeepEqual(iter, tc.iter) {
			t.Errorf("%s: %+v", tc.pattern, iter)
		}
	}
	// The plan is linear in the pattern, and bounded: a million nested
	// groups are refused, and captures in so many loops that dropping them
	// from a match would take a million checks are left as the engines
	// give them.
	deep := strings.Repeat("(", 1000000) + strings.Repeat(")+", 1000000)
	if _, _, iter, err := TransformRegExpIter(deep, false, false, false); err == nil || iter != nil {
		t.Errorf("nested: %v %v", iter != nil, err)
	}
	for _, tc := range []struct {
		captures int
		planned  bool
	}{{2000, true}, {2100, false}} {
		wide := strings.Repeat("(?:b|", 500) + strings.Repeat("(a)", tc.captures) + strings.Repeat(")+", 500)
		if _, _, iter, _ := TransformRegExpIter(wide, false, false, false); (iter != nil) != tc.planned {
			t.Errorf("wide, %d captures: planned %v", tc.captures, iter != nil)
		}
	}
}

// describeGroups describes Iterations.Groups (from 1), a word a group: h
// for a hidden one, r for one a backreference is to, then c, a, l, m, p
// and b for Consumed, Around, Loop, Marks, Parent and Before when they
// are set, with their values ("-" for none).
func describeGroups(groups []IterationGroup) string {
	var words []string
	for _, g := range groups[1:] {
		var w strings.Builder
		if g.Hidden {
			w.WriteString("h")
		}
		if g.Referenced {
			w.WriteString("r")
		}
		for _, f := range []struct {
			name  string
			value int
			set   bool
		}{{"c", g.Consumed, g.Consumed > 0}, {"a", g.Around, g.Around > 0}, {"l", g.Loop, g.Loop >= 0}, {"m", g.Marks, g.Marks >= 0}, {"p", g.Parent, g.Parent > 0}, {"b", g.Before, g.Before > 0}} {
			if f.set {
				w.WriteString(f.name + strconv.Itoa(f.value))
			}
		}
		if w.Len() == 0 {
			w.WriteString("-")
		}
		words = append(words, w.String())
	}
	return strings.Join(words, " ")
}

func TestTransformRegExpLeadingLoop(t *testing.T) {
	// A pattern that may start with an unbounded loop of a set of
	// characters starts, for regexp2, with a lookahead that always succeeds
	// (see leadingUnboundedLoop); Go's regexp gets it as it is.
	for _, tc := range []struct {
		pattern string
		leading bool
	}{
		{`[^\w]*?[ab]{1,2}b`, true}, {`(?=)\W*[ab]{2,}b`, true}, {`(?:)(?<=)((?:.+(?:ab|a)b))`, true}, {`(?<n>[a-c]{2,}b{1,2})`, true},
		{`a*b{0,1}`, true}, {`\p{L}*x{2,3}`, true}, {`(?<=)(?:[^x])*[ab]{2}[ab]{1,2}`, true},
		// Without a bounded repeat of variable length or an alternative, or
		// without a loop to start with, regexp2 finds the match by its
		// landmarks.
		{`[\w.]+@[\w.]+\.\w+`, false}, {`.*a{2}b`, false},
		{`^.*a{1,2}`, false}, {`(?=a).*b{1,2}`, false}, {`a.*b{1,2}`, false}, {`[ab]{1,2}b`, false}, {`(a)\1*a{1,2}`, false}, {`\ba*b{1,2}`, false},
	} {
		goText, r2, _, err := TransformRegExpIter(tc.pattern, false, true, false)
		if _, incompat := err.(RegexpErrorIncompatible); err != nil && !incompat {
			t.Fatalf("%s: %v", tc.pattern, err)
		}
		if got := strings.HasPrefix(r2, regexp2NoLandmarks); got != tc.leading || strings.Contains(goText, "(?!") {
			t.Errorf("%s: %q, %q", tc.pattern, goText, r2)
		}
	}
}
