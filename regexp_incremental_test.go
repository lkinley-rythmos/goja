package goja

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// ecmaFindAllSubmatchIndex is FindAllStringSubmatchIndex with the matches
// ECMAScript's global match has and Go's drops: an empty match right after
// a non-empty one. The pattern must not look before the position it
// matches at (see hasStartContext).
func ecmaFindAllSubmatchIndex(re *regexp.Regexp, s string, limit int) [][]int {
	var all [][]int
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		if n := len(all); n > 0 {
			prev := all[n-1]
			if e := prev[1]; e > prev[0] && m[0] > e {
				if loc := re.FindStringSubmatchIndex(s[e:]); loc != nil && loc[1] == 0 {
					for i := range loc {
						if loc[i] >= 0 {
							loc[i] += e
						}
					}
					all = append(all, loc)
				}
			}
		}
		all = append(all, m)
	}
	if n := len(all); n > 0 {
		if prev := all[n-1]; prev[1] > prev[0] {
			if e := prev[1]; e <= len(s) {
				if loc := re.FindStringSubmatchIndex(s[e:]); loc != nil && loc[1] == 0 {
					for i := range loc {
						if loc[i] >= 0 {
							loc[i] += e
						}
					}
					all = append(all, loc)
				}
			}
		}
	}
	if limit >= 0 && len(all) > limit {
		all = all[:limit]
	}
	return all
}

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
					want := submatchesToRegexpResults(ecmaFindAllSubmatchIndex(wrapped, in, limit), wrapped.SubexpNames())
					if sticky {
						pos := 0
						for i, r := range want {
							if r.indexes[0] != pos {
								want = want[:i]
								break
							}
							pos = nextStickyPos(in, r.indexes)
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
