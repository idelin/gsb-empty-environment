package main

import "testing"

func testConfig() SimConfig {
	return SimConfig{
		NumPackets: 4, PacketBytes: 100, Interval: 3,
		Rate1: 4, Prop1: 10, RTO: 60, LossEvery: 3,
		Rate2A: 5, Prop2A: 8, Rate2B: 2, Prop2B: 20,
		MaxAttempts: 5, MaxTicks: 100000,
	}
}

// The phase breakdown must sum exactly to the send-to-receive difference for
// every delivered packet; multipath overlap must not be double-added.
func TestBreakdownConsistentWithSendRecvDelta(t *testing.T) {
	res := RunSim(testConfig())
	bds := AnalyzePackets(res.Trace)
	if len(bds) == 0 {
		t.Fatal("no packet breakdowns")
	}
	for pkt, b := range bds {
		if !b.Delivered {
			t.Fatalf("pkt %d unexpectedly undelivered", pkt)
		}
		if !b.Consistent {
			t.Fatalf("pkt %d: phases sum %d != total %d (chain %v)",
				pkt, b.Sum, b.Total.Lo, b.Chain)
		}
		if b.Sum != b.Total.Lo || !b.Total.HiKnown {
			t.Fatalf("pkt %d: sum=%d total=%+v", pkt, b.Sum, b.Total)
		}
		want := res.Delivered[pkt] - int64(pkt)*testConfig().Interval
		if b.Total.Lo != want {
			t.Fatalf("pkt %d: total %d != deliver %d - enqueue %d", pkt, b.Total.Lo, res.Delivered[pkt], int64(pkt)*testConfig().Interval)
		}
		if got := b.Queue + b.Serial + b.Prop + b.RetransWait + b.Other; got != b.Total.Lo {
			t.Fatalf("pkt %d: components %d != deliver-enqueue %d", pkt, got, b.Total.Lo)
		}
	}
}

// Missing events yield bounds, and incrementally adding the missing events
// revises the bound into an exact result.
func TestMissingEventsGiveBoundsThenIncrementalRevision(t *testing.T) {
	tr := NewTrace()
	enq := tr.Add(Event{Tick: 0, Kind: KindEnqueue, Packet: 0, Link: "Q1"})
	deq := tr.Add(Event{Tick: 1, Kind: KindDequeue, Packet: 0, Link: "Q1", Parents: []int{enq}})
	ss := tr.Add(Event{Tick: 1, Kind: KindSendStart, Packet: 0, Link: "L1", Parents: []int{deq}})
	se := tr.Add(Event{Tick: 6, Kind: KindSendEnd, Packet: 0, Link: "L1", Parents: []int{ss}})

	b := AnalyzePackets(tr)[0]
	if b.Delivered {
		t.Fatal("packet reported delivered without a deliver event")
	}
	if b.Total.HiKnown {
		t.Fatalf("total = %+v, want open upper bound when deliver is missing", b.Total)
	}
	if b.Total.Lo != 6 {
		t.Fatalf("lower bound = %d, want 6 (last observed tick - enqueue)", b.Total.Lo)
	}

	// Incremental completion: recv + deliver arrive later.
	recv := tr.Add(Event{Tick: 9, Kind: KindRecv, Packet: 0, Link: "L1", Parents: []int{se}})
	tr.Add(Event{Tick: 9, Kind: KindDeliver, Packet: 0, Link: "DST", Parents: []int{recv}})

	b = AnalyzePackets(tr)[0]
	if !b.Delivered || !b.Total.HiKnown {
		t.Fatalf("after revision: delivered=%v total=%+v", b.Delivered, b.Total)
	}
	if b.Total.Lo != 9 || b.Total.Hi != 9 {
		t.Fatalf("total = %+v, want exactly 9", b.Total)
	}
	if !b.Consistent || b.Queue != 1 || b.Serial != 5 || b.Prop != 3 {
		t.Fatalf("breakdown = q%d s%d p%d consistent=%v, want q1 s5 p3 consistent",
			b.Queue, b.Serial, b.Prop, b.Consistent)
	}
}

// Lost packets contribute no success delay and report an open bound.
func TestLostPacketNoSuccessDelay(t *testing.T) {
	cfg := testConfig()
	cfg.LossEvery = 1 // every transmission dropped
	cfg.MaxAttempts = 1
	res := RunSim(cfg)
	if len(res.Delivered) != 0 {
		t.Fatalf("delivered %d packets, want 0", len(res.Delivered))
	}
	if len(res.Lost) != cfg.NumPackets {
		t.Fatalf("lost %d packets, want %d", len(res.Lost), cfg.NumPackets)
	}
	for pkt, b := range AnalyzePackets(res.Trace) {
		if b.Delivered {
			t.Fatalf("pkt %d marked delivered", pkt)
		}
		if b.Total.HiKnown {
			t.Fatalf("pkt %d: lost packet has closed total %+v", pkt, b.Total)
		}
	}
}

// Every phase keeps the IDs of the events it was derived from.
func TestEvidenceEventIDsPreserved(t *testing.T) {
	res := RunSim(testConfig())
	bds := AnalyzePackets(res.Trace)
	for pkt, b := range bds {
		if len(b.Chain) == 0 {
			t.Fatalf("pkt %d: empty causal chain", pkt)
		}
		for phase, ids := range b.Evidence {
			for _, id := range ids {
				if _, ok := res.Trace.Get(id); !ok {
					t.Fatalf("pkt %d phase %q references unknown event %d", pkt, phase, id)
				}
			}
		}
		if len(b.Evidence["serial"]) == 0 {
			t.Fatalf("pkt %d: no serial evidence", pkt)
		}
	}
}
