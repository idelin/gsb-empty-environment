package sim

import (
	"reflect"
	"testing"
)

func TestSchedulerTickOrder(t *testing.T) {
	s := NewScheduler()
	var got []string
	s.At(5, func() { got = append(got, "a@5") })
	s.At(1, func() { got = append(got, "b@1") })
	s.At(5, func() { got = append(got, "c@5") }) // same tick: FIFO after a@5
	s.At(9, func() { got = append(got, "d@9") })
	s.Run(100)
	want := []string{"b@1", "a@5", "c@5", "d@9"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if s.Now() != 9 {
		t.Fatalf("now = %d, want 9", s.Now())
	}
}

func TestSchedulerMaxTick(t *testing.T) {
	s := NewScheduler()
	var ran []int
	s.At(3, func() { ran = append(ran, 3) })
	s.At(10, func() { ran = append(ran, 10) })
	s.Run(5)
	if !reflect.DeepEqual(ran, []int{3}) {
		t.Fatalf("ran = %v, want [3]", ran)
	}
	if s.Pending() != 1 {
		t.Fatalf("pending = %d, want 1 (event beyond maxTick kept)", s.Pending())
	}
	s.Run(10)
	if !reflect.DeepEqual(ran, []int{3, 10}) {
		t.Fatalf("ran = %v, want [3 10]", ran)
	}
}

func TestSchedulerCascadingEvents(t *testing.T) {
	s := NewScheduler()
	count := 0
	var spawn func()
	spawn = func() {
		count++
		if count < 10 {
			s.After(2, spawn)
		}
	}
	s.After(1, spawn)
	s.Run(1000)
	if count != 10 {
		t.Fatalf("count = %d, want 10", count)
	}
}
