package cardinalguard

import (
	"testing"
)

// TestReplayFixture replays the hand-computed fixture. Its checkpoints pin
// down receive conservation, expired-generation isolation (late samples),
// whitelist bypass, label-key-order equivalence, correction transfers and
// fair FIFO promotion with exact counters.
func TestReplayFixture(t *testing.T) {
	events, err := LoadFixture("testdata/replay_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	l := NewLimiter(Config{
		TenantQuota: 3,
		MetricQuota: 2,
		TTL:         10,
		Whitelist:   []WhitelistEntry{{Tenant: "vip"}},
	})
	problems, err := Replay(l, events)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("replay: %s", p)
	}
}

// TestReplaySynthesizedWorkloads feeds deterministic synthesized workloads
// through the limiter and requires every invariant to hold after every
// single event.
func TestReplaySynthesizedWorkloads(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		events := SynthWorkload(seed, 3, 4, 10, 400)
		l := NewLimiter(Config{
			TenantQuota: 6,
			MetricQuota: 3,
			TTL:         15,
			Whitelist:   []WhitelistEntry{{Tenant: "tenant-0", Metric: "metric-0"}},
		})
		problems, err := Replay(l, events)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if len(problems) > 0 {
			t.Fatalf("seed %d: %d problems, first: %s", seed, len(problems), problems[0])
		}
		snap := l.Snapshot()
		if snap.Received != snap.Live+snap.Overflow+snap.Late+snap.Expired {
			t.Fatalf("seed %d: conservation broken: %+v", seed, snap)
		}
	}
}

// TestSynthIsDeterministic guarantees a synthesized workload can be captured
// as a fixture and replayed bit-for-bit.
func TestSynthIsDeterministic(t *testing.T) {
	a := SynthWorkload(42, 3, 4, 10, 200)
	b := SynthWorkload(42, 3, 4, 10, 200)
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Seq != b[i].Seq || a[i].Type != b[i].Type || a[i].Tick != b[i].Tick ||
			a[i].Tenant != b[i].Tenant || a[i].Metric != b[i].Metric || a[i].Bytes != b[i].Bytes {
			t.Fatalf("event %d differs between runs", i)
		}
	}
}
