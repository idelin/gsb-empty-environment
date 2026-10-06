package gateway

import (
	"math/big"
	"testing"

	"gwsim/ratelimit"
)

func rat(num, den int64) *big.Rat {
	return big.NewRat(num, den)
}

// A burst at the gateway is admitted up to the bucket capacity; the rest is
// rejected until enough rational refill has accumulated.
func TestSimulatorBurstThenRefill(t *testing.T) {
	l, err := ratelimit.NewLimiter(ratelimit.Config{
		Capacity: rat(4, 1),
		Rate:     rat(1, 2), // one token per 2 ns
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	sim := NewSimulator(l)

	reqs := []Request{
		{Time: 0, Cost: rat(1, 1)},
		{Time: 1, Cost: rat(1, 1)},
		{Time: 2, Cost: rat(1, 1)},
		{Time: 3, Cost: rat(1, 1)},
		{Time: 4, Cost: rat(1, 1)}, // bucket empty at t=4: 4 - 4 + 4*1/2 = 2... see below
		{Time: 4, Cost: rat(1, 1)},
		{Time: 4, Cost: rat(1, 1)},
		{Time: 100, Cost: rat(1, 1)},
	}
	// Exact balances: start 4.
	// t=0: 4-1=3 allow
	// t=1: 3+1/2=7/2, -1=5/2 allow
	// t=2: 5/2+1/2=3, -1=2 allow
	// t=3: 2+1/2=5/2, -1=3/2 allow
	// t=4: 3/2+1/2=2, -1=1 allow
	// t=4: 1, -1=0 allow
	// t=4: 0 < 1 reject
	// t=100: 0+96*1/2=48 -> truncate to 4, -1=3 allow
	want := []bool{true, true, true, true, true, true, false, true}

	decisions, err := sim.Run(reqs)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != len(want) {
		t.Fatalf("got %d decisions, want %d", len(decisions), len(want))
	}
	for i, d := range decisions {
		if d.Allowed != want[i] {
			t.Fatalf("decision %d: allowed = %v, want %v", i, d.Allowed, want[i])
		}
	}
	if got := l.Tokens(); got.Cmp(rat(3, 1)) != 0 {
		t.Fatalf("final tokens = %s, want 3", got)
	}
}

// A request stamped in the past is rejected and the simulator stops,
// leaving the limiter state untouched.
func TestSimulatorRejectsBackwardsRequest(t *testing.T) {
	l, err := ratelimit.NewLimiter(ratelimit.Config{
		Capacity: rat(4, 1),
		Rate:     rat(1, 1),
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	sim := NewSimulator(l)

	if _, err := sim.Run([]Request{{Time: 10, Cost: rat(1, 1)}}); err != nil {
		t.Fatal(err)
	}
	before := l.Tokens().String()

	decisions, err := sim.Run([]Request{{Time: 9, Cost: rat(1, 1)}})
	if err != ratelimit.ErrTimeBackwards {
		t.Fatalf("err = %v, want ErrTimeBackwards", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("got %d decisions before the error, want 0", len(decisions))
	}
	if l.Tokens().String() != before || l.Last() != 10 {
		t.Fatalf("state changed after backwards request")
	}
}
