package goja

import (
	"runtime"
	"strings"
	"testing"
)

var uncachedPatterns = []struct{ pattern, flags string }{
	{"a", ""},
	{"a{1000}", ""},
	{"a{1000}", "gy"},
	{`[\p{L}\P{L}]`, "iu"},
	{`(?<=a)b`, ""},         // regexp2 only: lookbehind
	{`(a)\1`, "i"},          // regexp2 only: a backreference
	{"(a{1000}){1000}", ""}, // too large for Go's regexp: regexp2
	{"a{2147483648}", ""},   // a count regexp2 refuses
	{"(", ""},
	{"a{2,1}", ""},
	{"[b-a]", ""},
	{`\p{Foo}`, "u"},
	{"(?<n>a)(?<n>b)", ""},
	{"a", "gg"},
	{"a", "v"},
	{strings.Repeat(`\p{L}`, 40000), "u"}, // past the compile cost bound
	{"x" + strings.Repeat("a(b|", 500) + "b" + strings.Repeat(")", 500), "iu"},
}

// CompileRegExpUncached refuses what the cached compile (the one literals
// and the RegExp constructor use) refuses, with the same error, and leaves
// the cache as it was.
func TestCompileRegExpUncachedAsCached(t *testing.T) {
	ResetRegexpCache()
	defer ResetRegexpCache()
	for _, p := range uncachedPatterns {
		_, cachedErr := compileRegexp(p.pattern, p.flags)
		entries := regexpCacheLen()
		for range 2 {
			err := CompileRegExpUncached(p.pattern, p.flags)
			if (err == nil) != (cachedErr == nil) || err != nil && err.Error() != cachedErr.Error() {
				t.Errorf("/%.40s/%s: uncached %v, cached %v", p.pattern, p.flags, err, cachedErr)
			}
		}
		if n := regexpCacheLen(); n != entries {
			t.Errorf("/%.40s/%s: the cache went from %d patterns to %d", p.pattern, p.flags, entries, n)
		}
	}
	ResetRegexpCache()
	for _, p := range uncachedPatterns {
		_ = CompileRegExpUncached(p.pattern, p.flags)
	}
	if n := regexpCacheLen(); n != 0 {
		t.Errorf("an empty cache holds %d patterns after uncached compiles", n)
	}
}

// With the pattern in the cache, a literal's compile costs a copy of the
// template; CompileRegExpUncached still pays the whole compile.
func TestCompileRegExpUncachedPaysTheWholeCompile(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("allocation under the race detector")
	}
	ResetRegexpCache()
	defer ResetRegexpCache()
	allocated := func(f func()) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		f()
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	for _, p := range []struct{ pattern, flags string }{
		{strings.Repeat("a{1000}", 20), ""},
		{"[" + strings.Repeat(`\P{Lo}`, 20) + "]", "iu"},
		{strings.Repeat("()", 2000) + `\1`, "i"},
	} {
		source := "x=/" + p.pattern + "/" + p.flags
		cold := allocated(func() { MustCompile("", source, false) })
		warm := allocated(func() { MustCompile("", source, false) })
		uncached := allocated(func() {
			if err := CompileRegExpUncached(p.pattern, p.flags); err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("/%.30s/%s: cold literal %d, warm literal %d, uncached %d", p.pattern, p.flags, cold, warm, uncached)
		if uncached < cold*8/10 || uncached < 10*warm {
			t.Errorf("/%.30s/%s: uncached compile allocated %d (cold literal %d, warm %d)", p.pattern, p.flags, uncached, cold, warm)
		}
	}
}
