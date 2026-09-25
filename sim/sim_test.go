package sim

import (
	"reflect"
	"testing"
)

// TestDeterministicOrdering: same seed and same schedule => identical
// event order and identical virtual-time trace.
func TestDeterministicOrdering(t *testing.T) {
	run := func() []string {
		eng := NewEngine()
		eng.Trace = true
		net := NewNet(eng, 99)
		net.DropProb = 0.3
		net.DupProb = 0.3
		net.Register("a", func(interface{}) {})
		for i := 0; i < 50; i++ {
			net.Send("x", "a", "m", i)
		}
		eng.Run()
		return eng.TraceL
	}
	if a, b := run(), run(); !reflect.DeepEqual(a, b) {
		t.Fatal("same seed produced different traces")
	}
}

// TestVirtualTime: events execute in non-decreasing virtual time and
// After(d) lands exactly d ticks in the future.
func TestVirtualTime(t *testing.T) {
	eng := NewEngine()
	var times []int64
	eng.After(5, "a", func() { times = append(times, eng.Now()) })
	eng.After(2, "b", func() {
		times = append(times, eng.Now())
		eng.After(3, "c", func() { times = append(times, eng.Now()) })
	})
	eng.Run()
	want := []int64{2, 5, 5}
	if !reflect.DeepEqual(times, want) {
		t.Fatalf("times=%v, want %v", times, want)
	}
}
