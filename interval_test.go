package netsec

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func iv(low, high int64) Interval {
	return Interval{Low: low, High: high}
}

func TestNewIntervalSetNormalizes(t *testing.T) {
	got := NewIntervalSet(iv(10, 20), iv(1, 5), iv(5, 9), iv(30, 40), iv(39, 42))
	want := IntervalSet{ranges: []Interval{iv(1, 20), iv(30, 42)}}
	if !got.Equal(want) {
		t.Fatalf("normalize = %v, want %v", got.Ranges(), want.Ranges())
	}
}

func TestIntervalSetContainsAndEqual(t *testing.T) {
	set := NewIntervalSet(iv(1, 4), iv(10, 10))
	cases := map[int64]bool{0: false, 1: true, 4: true, 5: false, 10: true, 11: false}
	for resource, want := range cases {
		if got := set.Contains(resource); got != want {
			t.Errorf("Contains(%d) = %v, want %v", resource, got, want)
		}
	}
	if set.Equal(NewIntervalSet(iv(10, 10), iv(1, 4))) != true {
		t.Error("equivalent sets should be equal regardless of input order")
	}
	if set.Equal(NewIntervalSet(iv(1, 4))) {
		t.Error("different sets must not be equal")
	}
}

func TestIntervalSetUnionIntersectSubtract(t *testing.T) {
	left := NewIntervalSet(iv(1, 10), iv(20, 30))
	right := NewIntervalSet(iv(5, 25))

	if got := left.Union(right); !got.Equal(NewIntervalSet(iv(1, 30))) {
		t.Errorf("union = %v", got.Ranges())
	}
	if got := left.Intersect(right); !got.Equal(NewIntervalSet(iv(5, 10), iv(20, 25))) {
		t.Errorf("intersect = %v", got.Ranges())
	}
	if got := left.Subtract(right); !got.Equal(NewIntervalSet(iv(1, 4), iv(26, 30))) {
		t.Errorf("subtract = %v", got.Ranges())
	}
}

func TestIntervalSetSubtractOverflowBoundaries(t *testing.T) {
	full := NewIntervalSet(iv(math.MinInt64, math.MaxInt64))
	tail := NewIntervalSet(iv(math.MinInt64, 0))
	got := full.Subtract(tail)
	want := NewIntervalSet(iv(1, math.MaxInt64))
	if !got.Equal(want) {
		t.Fatalf("subtract overflow edge = %v, want %v", got.Ranges(), want.Ranges())
	}
}

func TestIntervalSetJSONRoundTripStable(t *testing.T) {
	set := NewIntervalSet(iv(30, 40), iv(1, 9), iv(10, 20))
	data, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `[{"low":1,"high":20},{"low":30,"high":40}]`
	if string(data) != wantJSON {
		t.Fatalf("marshaled %s, want %s", data, wantJSON)
	}
	var decoded IntervalSet
	if err := json.Unmarshal([]byte(`[{"low":40,"high":30},{"low":1,"high":5}]`), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Ranges(), []Interval{iv(1, 5)}) {
		t.Fatalf("invalid interval should be dropped, got %v", decoded.Ranges())
	}
	var empty IntervalSet
	if data, err := json.Marshal(empty); err != nil || string(data) != "[]" {
		t.Fatalf("empty set marshal = %s, %v", data, err)
	}
}
