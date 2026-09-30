package streamsim

import (
	"reflect"
	"testing"
)

func TestEventOrdering(t *testing.T) {
	sim := NewSimulator()
	var got []string
	sim.At(5, func() { got = append(got, "t5") })
	sim.At(1, func() { got = append(got, "t1") })
	sim.At(3, func() { got = append(got, "t3a") })
	sim.At(3, func() { got = append(got, "t3b") }) // same tick: FIFO
	sim.At(1, func() { got = append(got, "t1b") })
	sim.Run()
	want := []string{"t1", "t1b", "t3a", "t3b", "t5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if sim.Now() != 5 {
		t.Fatalf("now = %d, want 5", sim.Now())
	}
}

func TestLinkDelayAndSerialization(t *testing.T) {
	sim := NewSimulator()
	var ticks []uint64
	link := NewLink(sim, 2, 3, func(Packet) { ticks = append(ticks, sim.Now()) })
	p := Packet{Tuple: cli}
	sim.At(0, func() { link.Send(p) })
	sim.At(0, func() { link.Send(p) })
	sim.Run()
	// pkt1: dequeued at 3, delivered at 5; pkt2: dequeued at 6, delivered at 8.
	if want := []uint64{5, 8}; !reflect.DeepEqual(ticks, want) {
		t.Fatalf("delivery ticks = %v, want %v", ticks, want)
	}
}

func TestLinkQueueDrop(t *testing.T) {
	sim := NewSimulator()
	var delivered int
	link := NewLink(sim, 1, 10, func(Packet) { delivered++ })
	link.MaxQueue = 1
	p := Packet{Tuple: cli}
	sim.At(0, func() { link.Send(p); link.Send(p); link.Send(p) })
	sim.Run()
	if link.Dropped != 2 {
		t.Fatalf("dropped = %d, want 2", link.Dropped)
	}
	if delivered != 1 {
		t.Fatalf("delivered = %d, want 1", delivered)
	}
}

func TestEndToEndThroughLink(t *testing.T) {
	sim := NewSimulator()
	tb := NewTable()
	link := NewLink(sim, 5, 1, func(p Packet) { tb.Handle(p) })
	sim.At(0, func() { link.Send(syn(cli, 0, 1000)) })
	sim.At(1, func() { link.Send(data(cli, 0, 1001, "ping")) })
	sim.At(2, func() { link.Send(fin(cli, 0, 1005)) })
	sim.Run()
	c := tb.Conns()[0]
	if got := string(c.Deliver(ClientToServer)); got != "ping" {
		t.Fatalf("deliver = %q, want %q", got, "ping")
	}
	if !c.FinReceived(ClientToServer) {
		t.Fatal("expected FIN")
	}
	if chunks := c.Finalize(ClientToServer); len(chunks) != 0 {
		t.Fatalf("finalize = %+v, want no gaps", chunks)
	}
}
