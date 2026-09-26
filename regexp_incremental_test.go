package goja

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestGlobalMatch_IncrementalSameAsFindAll(t *testing.T) {
	inputs := []string{
		"", "a", "aaa", "abcabc", "a,b,,c,", "héllo wörld ☃ x", "\xff\xfeab\xff", "aa\nbb\n\ncc",
		strings.Repeat("xyz ", 50),
	}
	patterns := []struct{ src, flags string }{
		{`.`, "g"}, {`a*`, "g"}, {`(?:)`, "g"}, {`b|`, "g"}, {`(a)|(b)`, "g"}, {`(\w+)(,)?`, "g"}, {`x*y?`, "g"},
		{`\s*`, "g"}, {`.`, "gs"}, {`[^a]*`, "g"}, {`c$`, "g"}, {`$`, "gm"}, {`$`, "g"}, {`é|ö`, "g"}, {`[a-z]`, "gi"},
	}
	for _, pt := range patterns {
		pat := pt.src + "/" + pt.flags
		p, err := compileRegexp(pt.src, pt.flags)
		if err != nil {
			t.Fatal(err)
		}
		if p.regexpWrapper == nil || p.goStartContext {
			t.Fatalf("%s: not a context-free Go regexp", pat)
		}
		wrapped := (*regexp.Regexp)(p.regexpWrapper)
		vm := New().vm
		for _, in := range inputs {
			for _, limit := range []int{-1, 0, 1, 2, 5} {
				for _, sticky := range []bool{false, true} {
					want := submatchesToRegexpResults(wrapped.FindAllStringSubmatchIndex(in, limit), wrapped.SubexpNames())
					if sticky {
						pos := 0
						for i, r := range want {
							if r.indexes[0] != pos {
								want = want[:i]
								break
							}
							pos = r.indexes[1]
						}
					}
					got := p.regexpWrapper.findAllIncremental(vm, in, limit, sticky, p.prefixSkip)
					if len(got) != len(want) {
						t.Fatalf("%q on %q (limit %d, sticky %v): %d matches, want %d", pat, in, limit, sticky, len(got), len(want))
					}
					for i := range got {
						if !slices.Equal(got[i].indexes, want[i].indexes) || !slices.Equal(got[i].groups, want[i].groups) {
							t.Fatalf("%q on %q (limit %d, sticky %v): match %d is %v, want %v", pat, in, limit, sticky, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
	for _, pat := range []string{`^a`, `\ba`, `\Ba`, `x|^b`} {
		p, err := compileRegexp(pat, "gm")
		if err != nil {
			t.Fatal(err)
		}
		if !p.goStartContext {
			t.Errorf("%s: goStartContext not set", pat)
		}
	}
}
