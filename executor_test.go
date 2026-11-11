package netsec

import (
	"errors"
	"testing"
)

func sampleInput() Input {
	return Input{
		Requests: []Request{
			{ID: "r1", KeyPurpose: "sign", Capacity: 2},
			{ID: "r2", KeyPurpose: "sign", Capacity: 3},
			{ID: "r3", KeyPurpose: "wrap", Capacity: 1},
		},
		Domains: []Domain{
			{ID: "d1", Purposes: map[string]int{"sign": 5, "wrap": 4}},
		},
		Cost: [][]int{{1}, {1}, {1}},
	}
}

func TestExecutorReservesAllOrNothing(t *testing.T) {
	in := sampleInput()
	plan, _, err := Solve(in)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}

	store := NewMemCapacityStore(in.Domains)
	executor := NewExecutor(store)

	reservations, err := executor.Execute(in, plan)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(reservations) != 2 {
		t.Fatalf("expected two aggregated reservations, got %d: %+v",
			len(reservations), reservations)
	}

	if got := store.Remaining("d1", "sign"); got != 0 {
		t.Fatalf("expected sign pool exhausted, remaining=%d", got)
	}
	if got := store.Remaining("d1", "wrap"); got != 3 {
		t.Fatalf("expected 3 wrap units remaining, got %d", got)
	}
}

// failingReserver rejects a configured reservation and records every call so
// the test can assert rollback released all earlier reservations.
type failingReserver struct {
	store      *MemCapacityStore
	failOn     Reservation
	reserveLog []Reservation
	releaseLog []Reservation
}

func (f *failingReserver) Reserve(r Reservation) error {
	f.reserveLog = append(f.reserveLog, r)
	if r.DomainID == f.failOn.DomainID && r.Purpose == f.failOn.Purpose {
		return errors.New("hardware rejected reservation")
	}
	return f.store.Reserve(r)
}

func (f *failingReserver) Release(r Reservation) error {
	f.releaseLog = append(f.releaseLog, r)
	return f.store.Release(r)
}

func TestExecutorRollsBackOnFailure(t *testing.T) {
	in := Input{
		Requests: []Request{
			{ID: "r1", KeyPurpose: "sign", Capacity: 2},
			{ID: "r2", KeyPurpose: "wrap", Capacity: 3},
		},
		Domains: []Domain{
			{ID: "d1", Purposes: map[string]int{"sign": 5, "wrap": 5}},
		},
		Cost: [][]int{{1}, {1}},
	}
	plan, _, err := Solve(in)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}

	store := NewMemCapacityStore(in.Domains)
	backend := &failingReserver{
		store:  store,
		failOn: Reservation{DomainID: "d1", Purpose: "wrap"},
	}
	executor := NewExecutor(backend)

	if _, err := executor.Execute(in, plan); err == nil {
		t.Fatal("expected execution failure")
	}
	if len(backend.reserveLog) != 2 || len(backend.releaseLog) != 1 {
		t.Fatalf("expected 2 reserves and 1 rollback, got reserves=%v releases=%v",
			backend.reserveLog, backend.releaseLog)
	}
	if got := store.Remaining("d1", "sign"); got != 5 {
		t.Fatalf("sign pool must be fully rolled back, remaining=%d", got)
	}
	if got := store.Remaining("d1", "wrap"); got != 5 {
		t.Fatalf("wrap pool must be untouched, remaining=%d", got)
	}
}

func TestExecutorRejectsTamperedPlan(t *testing.T) {
	in := sampleInput()
	store := NewMemCapacityStore(in.Domains)
	executor := NewExecutor(store)

	if _, err := executor.Execute(in, nil); err == nil {
		t.Fatal("expected nil plan error")
	}

	short := &Plan{Assignment: []int{0, 0}}
	if _, err := executor.Execute(in, short); err == nil {
		t.Fatal("expected assignment length error")
	}

	outOfRange := &Plan{Assignment: []int{0, 0, 7}}
	if _, err := executor.Execute(in, outOfRange); err == nil {
		t.Fatal("expected out-of-range domain error")
	}

	negative := &Plan{Assignment: []int{-1, 0, 0}}
	if _, err := executor.Execute(in, negative); err == nil {
		t.Fatal("expected negative assignment error")
	}
}

func TestMemStoreRejectsOversubscription(t *testing.T) {
	store := NewMemCapacityStore([]Domain{
		{ID: "d1", Purposes: map[string]int{"k": 2}},
	})
	if err := store.Reserve(Reservation{DomainID: "d1", Purpose: "k", Amount: 3}); !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("expected ErrInsufficientCapacity, got %v", err)
	}
	if got := store.Remaining("d1", "k"); got != 2 {
		t.Fatalf("failed reservation must not consume capacity, got %d", got)
	}
}
