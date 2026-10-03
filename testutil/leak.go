// Package testutil provides shared assertions for the fault-tolerance
// test suites.
package testutil

import (
	"runtime"
	"testing"
	"time"
)

// AssertNoGoroutineLeak records the goroutine count and, after the returned
// function is called, fails t if goroutines remain after a settle window.
//
//	func TestX(t *testing.T) {
//	    defer testutil.AssertNoGoroutineLeak(t)()
//	    ...
//	}
func AssertNoGoroutineLeak(t *testing.T) func() {
	before := runtime.NumGoroutine()
	return func() {
		var after int
		for i := 0; i < 50; i++ {
			after = runtime.NumGoroutine()
			if after <= before {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Errorf("goroutine leak: before=%d after=%d", before, after)
	}
}
