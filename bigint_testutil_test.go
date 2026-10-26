package goja

import (
	"runtime/debug"
	"testing"
	"time"
)

// slack is a time bound of a test as it holds under the race detector,
// which runs these BigInt and compile loops 10 to 20 times slower.
func slack(d time.Duration) time.Duration {
	if raceDetectorEnabled {
		return 25 * d
	}
	return d
}

// skipHeavy skips a test that makes values of hundreds of megabytes, with
// -short and under the race detector (whose shadow memory would take the
// run past a 6 GiB cap: the strings of 2^28 units and more of the string
// and global-match limit tests took a capped go test -race -short ./... to
// 5.0 GiB, and its root package to 591 s of go test's 600).
//
// What a heavy test made is given back to the system when it ends: the
// runtime kept it (TestStringToBigIntMaxSize grows the process by 3.9
// GiB), and the tests after it, which make gigabytes of their own
// (TestNativeDepth_JSONParseDeepInput, TestNativeLoops_ObserveInterrupt),
// started from more.
func skipHeavy(t *testing.T) {
	t.Helper()
	if testing.Short() || raceDetectorEnabled {
		t.Skip("makes values of hundreds of megabytes")
	}
	freeAfter(t)
}

// freeAfter gives what t made back to the system when it ends (see
// skipHeavy).
func freeAfter(t *testing.T) {
	t.Cleanup(debug.FreeOSMemory)
}

// bigIntStepSlack is how long past an interrupt a BigInt operation may run
// (one step of about 50 ms at most, with margin).
var bigIntStepSlack = slack(60 * time.Millisecond)
