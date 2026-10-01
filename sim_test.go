package dvsim

import (
	"reflect"
	"testing"
)

func TestEventOrderByTick(t *testing.T) {
	sim := NewSimulator()
	var got []string
	sim.Schedule(5, func() { got = append(got, "t5") })
	sim.Schedule(1, func() { got = append(got, "t1") })
	sim.Schedule(3, func() { got = append(got, "t3") })
	sim.Run(100)
	want := []string{"t1", "t3", "t5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if sim.Now() != 5 {
		t.Fatalf("now = %d, want 5", sim.Now())
	}
}

func TestSameTickFIFO(t *testing.T) {
	sim := NewSimulator()
	var got []int
	for i := 0; i < 5; i++ {
		i := i
		sim.Schedule(2, func() { got = append(got, i) })
	}
	sim.Run(100)
	if !reflect.DeepEqual(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("got %v", got)
	}
}

func TestRunStopsAtUntilTick(t *testing.T) {
	sim := NewSimulator()
	ran := false
	sim.Schedule(10, func() { ran = true })
	sim.Run(5)
	if ran {
		t.Fatal("event beyond untilTick must not run")
	}
	if sim.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", sim.Pending())
	}
	sim.Run(10)
	if !ran {
		t.Fatal("event should run once untilTick covers it")
	}
}

func TestNegativeDelayClamped(t *testing.T) {
	sim := NewSimulator()
	ran := false
	sim.Schedule(-3, func() { ran = true })
	sim.Run(0)
	if !ran {
		t.Fatal("negative delay should run at current tick")
	}
}
