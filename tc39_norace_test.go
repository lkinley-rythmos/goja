//go:build !race
// +build !race

package goja

import "testing"

// Prevent linter warnings about unused type
var _ = tc39Test{name: "", f: nil}

// raceDetectorEnabled reports whether this binary was built with -race.
// sync.Pool caching is disabled under the race detector, which shifts
// exact allocation counts on pool-dependent paths.
const raceDetectorEnabled = false

func (ctx *tc39TestCtx) runTest(name string, f func(t *testing.T)) {
	ctx.t.Run(name, func(t *testing.T) {
		t.Parallel()
		f(t)
	})
}

func (ctx *tc39TestCtx) flush() {
}
