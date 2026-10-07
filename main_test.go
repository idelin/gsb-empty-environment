package main

import (
	"math/rand"
	"testing"
)

func TestSimulatorCrossCheck(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	if err := crossCheckSimulators(rnd, 5000); err != nil {
		t.Fatal(err)
	}
}

func TestExpiryBoundary(t *testing.T) {
	if err := verifyExpiryBoundary(); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministicVsBruteForce(t *testing.T) {
	rnd := rand.New(rand.NewSource(2))
	n, err := verifyDeterministic(rnd, 300)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no feasible instances checked")
	}
	t.Logf("checked %d feasible instances", n)
}

func TestStochasticVsBruteForce(t *testing.T) {
	rnd := rand.New(rand.NewSource(3))
	n, err := verifyStochastic(rnd, 60)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no feasible instances checked")
	}
	t.Logf("checked %d feasible instances", n)
}

func TestDeterministicDemoInstance(t *testing.T) {
	p := DetParams{
		T:      10,
		Shelf:  3,
		Cap:    6,
		Setup:  12,
		Unit:   2,
		Hold:   1,
		Demand: []int{4, 3, 2, 5, 1, 0, 3, 2, 4, 6},
	}
	cost, plan, ok := solveDeterministic(p)
	if !ok {
		t.Fatal("demo instance infeasible")
	}
	if got := ledgerCost(buildLedger(p, plan)); got != cost {
		t.Fatalf("ledger cost %d != dp cost %d", got, cost)
	}
	// Feasibility: no stockout along the plan, capacity respected.
	inv := make([]int, p.Shelf-1)
	for day := 0; day < p.T; day++ {
		if plan[day] > p.Cap {
			t.Fatalf("capacity violated on day %d", day+1)
		}
		next, _, _, feasible := simulateDay(inv, plan[day], p.Demand[day], p.Shelf)
		if !feasible {
			t.Fatalf("stockout on day %d", day+1)
		}
		inv = next
	}
}
