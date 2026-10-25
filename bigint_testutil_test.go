package goja

import (
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
// run past a 6 GiB cap).
func skipHeavy(t *testing.T) {
	t.Helper()
	if testing.Short() || raceDetectorEnabled {
		t.Skip("makes values of hundreds of megabytes")
	}
}

// bigIntStepSlack is how long past an interrupt a BigInt operation may run
// (one step of about 50 ms at most, with margin).
var bigIntStepSlack = slack(60 * time.Millisecond)
