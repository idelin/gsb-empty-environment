package netsec

import (
	"encoding/json"
	"testing"
)

func FuzzIntervalSetInvariants(f *testing.F) {
	f.Add(int64(1), int64(10), int64(5), int64(8), int64(20), int64(12))
	f.Fuzz(func(t *testing.T, aLow, aHigh, bLow, bHigh, point, probe int64) {
		if aLow > aHigh {
			aLow, aHigh = aHigh, aLow
		}
		if bLow > bHigh {
			bLow, bHigh = bHigh, bLow
		}
		left := NewIntervalSet(iv(aLow, aHigh))
		right := NewIntervalSet(iv(bLow, bHigh))

		ranges := left.Union(right).Ranges()
		for i := 1; i < len(ranges); i++ {
			if ranges[i-1].High >= ranges[i].Low {
				t.Fatalf("union not normalized: %v", ranges)
			}
		}

		intersection := left.Intersect(right)
		if !intersection.Equal(right.Intersect(left)) {
			t.Fatal("intersection must be commutative")
		}
		diff := left.Subtract(right)
		if !diff.Intersect(right).Equal(IntervalSet{}) {
			t.Fatalf("subtract result overlaps the removed set: %v", diff.Ranges())
		}

		data, err := json.Marshal(NewIntervalSet(iv(aLow, aHigh), iv(bLow, bHigh)))
		if err != nil {
			t.Fatal(err)
		}
		var decoded IntervalSet
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if !decoded.Equal(left.Union(right)) {
			t.Fatal("JSON round-trip changed the set")
		}

		// Contains must always agree with the stored ranges.
		set := NewIntervalSet(iv(aLow, aHigh), iv(bLow, bHigh), iv(point, probe))
		manual := false
		for _, r := range set.Ranges() {
			if r.Low <= point && point <= r.High {
				manual = true
			}
		}
		if set.Contains(point) != manual {
			t.Fatalf("Contains(%d) disagrees with ranges %v", point, set.Ranges())
		}
	})
}
