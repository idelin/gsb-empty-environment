package streamsim

import (
	"reflect"
	"testing"
)

var (
	cli = Tuple{SrcIP: "10.0.0.1", SrcPort: 5000, DstIP: "10.0.0.2", DstPort: 80}
	srv = cli.Reverse()
)

func data(t Tuple, epoch uint32, seq uint32, s string) Packet {
	return Packet{Tuple: t, Epoch: epoch, Seq: seq, Data: []byte(s)}
}

func syn(t Tuple, epoch uint32, isn uint32) Packet {
	return Packet{Tuple: t, Epoch: epoch, Seq: isn, Syn: true}
}

func fin(t Tuple, epoch uint32, seq uint32) Packet {
	return Packet{Tuple: t, Epoch: epoch, Seq: seq, Fin: true}
}

func TestInOrderDelivery(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1001, "abc"))
	tb.Handle(data(cli, 0, 1004, "def"))
	c := tb.Conns()[0]
	if got := string(c.Deliver(ClientToServer)); got != "abcdef" {
		t.Fatalf("deliver = %q, want %q", got, "abcdef")
	}
	if got := c.Deliver(ClientToServer); got != nil {
		t.Fatalf("second deliver = %q, want nil", got)
	}
}

func TestOutOfOrderThenGapFilled(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1004, "def"))
	c := tb.Conns()[0]
	if got := c.Deliver(ClientToServer); got != nil {
		t.Fatalf("deliver with gap = %q, want nil", got)
	}
	tb.Handle(data(cli, 0, 1001, "abc"))
	if got := string(c.Deliver(ClientToServer)); got != "abcdef" {
		t.Fatalf("deliver after fill = %q, want %q", got, "abcdef")
	}
}

func TestIdenticalRetransmit(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1001, "abcdef"))
	tb.Handle(data(cli, 0, 1003, "cde")) // overlapping retransmit, same bytes
	c := tb.Conns()[0]
	if n := len(c.Conflicts(ClientToServer)); n != 0 {
		t.Fatalf("conflicts = %d, want 0", n)
	}
	if got := string(c.Deliver(ClientToServer)); got != "abcdef" {
		t.Fatalf("deliver = %q, want %q", got, "abcdef")
	}
}

func TestConflictingOverlap(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1001, "abcdef"))
	tb.Handle(data(cli, 0, 1003, "cXe")) // 'X' lands at offset 3, differs from 'd'
	c := tb.Conns()[0]
	cfs := c.Conflicts(ClientToServer)
	if len(cfs) != 1 {
		t.Fatalf("conflicts = %v, want exactly 1", cfs)
	}
	want := Conflict{Offset: 3, Existing: 'd', Incoming: 'X'}
	if cfs[0] != want {
		t.Fatalf("conflict = %+v, want %+v", cfs[0], want)
	}
	// First-seen bytes win.
	if got := string(c.Deliver(ClientToServer)); got != "abcdef" {
		t.Fatalf("deliver = %q, want %q", got, "abcdef")
	}
}

func TestGapReportedNotFabricated(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1001, "abc"))
	tb.Handle(data(cli, 0, 1007, "ghi")) // gap at offsets 3..6
	tb.Handle(fin(cli, 0, 1010))
	c := tb.Conns()[0]
	if got := string(c.Deliver(ClientToServer)); got != "abc" {
		t.Fatalf("deliver = %q, want %q", got, "abc")
	}
	chunks := c.Finalize(ClientToServer)
	if len(chunks) != 2 {
		t.Fatalf("finalize chunks = %v, want 2", chunks)
	}
	if chunks[0].Gap == nil || *chunks[0].Gap != (Gap{Start: 3, End: 6}) {
		t.Fatalf("chunk[0] = %+v, want gap [3,6)", chunks[0])
	}
	if string(chunks[1].Data) != "ghi" {
		t.Fatalf("chunk[1] = %q, want %q", chunks[1].Data, "ghi")
	}
	// Total output bytes must equal received bytes: nothing invented.
	total := 0
	for _, ch := range chunks {
		total += len(ch.Data)
	}
	if total != 3 {
		t.Fatalf("finalized data bytes = %d, want 3 (no fabrication)", total)
	}
}

func TestHalfClose(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1001, "abc"))
	tb.Handle(fin(cli, 0, 1004)) // client half-closes
	tb.Handle(data(cli, 0, 1004, "zzz"))
	tb.Handle(data(srv, 0, 2000, "ok")) // other direction still open
	c := tb.Conns()[0]
	if !c.FinReceived(ClientToServer) {
		t.Fatal("expected FIN on client->server")
	}
	if c.FinReceived(ServerToClient) {
		t.Fatal("unexpected FIN on server->client")
	}
	if n := c.DataAfterFin(ClientToServer); n != 3 {
		t.Fatalf("data after FIN = %d, want 3", n)
	}
	if got := string(c.Deliver(ServerToClient)); got != "ok" {
		t.Fatalf("server->client deliver = %q, want %q", got, "ok")
	}
	if c.Closed() {
		t.Fatal("conn should be half-closed, not closed")
	}
	tb.Handle(fin(srv, 0, 2002))
	if !c.Closed() {
		t.Fatal("conn should be fully closed after both FINs")
	}
}

func TestEpochIsolation(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1001, "old"))
	// New incarnation reuses the same 4-tuple.
	tb.Handle(syn(cli, 1, 5000))
	tb.Handle(data(cli, 1, 5001, "new"))
	// Late retransmit from the old epoch must not mix in.
	tb.Handle(data(cli, 0, 1001, "OLD"))
	if tb.StaleDrops != 1 {
		t.Fatalf("stale drops = %d, want 1", tb.StaleDrops)
	}
	conns := tb.Conns()
	if len(conns) != 2 {
		t.Fatalf("conns = %d, want 2", len(conns))
	}
	var e0, e1 *Conn
	for _, c := range conns {
		if c.Epoch == 0 {
			e0 = c
		} else {
			e1 = c
		}
	}
	if got := string(e0.Deliver(ClientToServer)); got != "old" {
		t.Fatalf("epoch0 deliver = %q, want %q", got, "old")
	}
	if got := string(e1.Deliver(ClientToServer)); got != "new" {
		t.Fatalf("epoch1 deliver = %q, want %q", got, "new")
	}
	if n := len(e1.Conflicts(ClientToServer)); n != 0 {
		t.Fatalf("epoch1 conflicts = %d, want 0 (stale packet must not mix)", n)
	}
}

func TestIndependentSequenceSpaces(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 100))
	tb.Handle(data(cli, 0, 101, "req"))
	tb.Handle(data(srv, 0, 9000, "res"))
	c := tb.Conns()[0]
	if got := string(c.Deliver(ClientToServer)); got != "req" {
		t.Fatalf("c2s = %q, want %q", got, "req")
	}
	if got := string(c.Deliver(ServerToClient)); got != "res" {
		t.Fatalf("s2c = %q, want %q", got, "res")
	}
}

func TestFinalizeTrailingGapBeforeFin(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1001, "ab"))
	tb.Handle(fin(cli, 0, 1008)) // FIN claims bytes 1003..1008 never arrived
	c := tb.Conns()[0]
	c.Deliver(ClientToServer)
	chunks := c.Finalize(ClientToServer)
	if len(chunks) != 1 || chunks[0].Gap == nil {
		t.Fatalf("chunks = %+v, want single trailing gap", chunks)
	}
	if *chunks[0].Gap != (Gap{Start: 2, End: 7}) {
		t.Fatalf("gap = %v, want [2,7)", chunks[0].Gap)
	}
}

func TestRetransmitAfterDeliver(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1001, "abc"))
	c := tb.Conns()[0]
	c.Deliver(ClientToServer)
	// Already-delivered retransmit is ignored silently.
	tb.Handle(data(cli, 0, 1001, "abc"))
	if got := c.Deliver(ClientToServer); got != nil {
		t.Fatalf("deliver = %q, want nil", got)
	}
	if n := len(c.Conflicts(ClientToServer)); n != 0 {
		t.Fatalf("conflicts = %d, want 0", n)
	}
}

func TestFinalizeChunksAreOrdered(t *testing.T) {
	tb := NewTable()
	tb.Handle(syn(cli, 0, 1000))
	tb.Handle(data(cli, 0, 1001, "aa"))
	tb.Handle(data(cli, 0, 1005, "bb"))
	tb.Handle(data(cli, 0, 1009, "cc"))
	c := tb.Conns()[0]
	chunks := c.Finalize(ClientToServer)
	var kinds []string
	for _, ch := range chunks {
		if ch.Gap != nil {
			kinds = append(kinds, "gap")
		} else {
			kinds = append(kinds, string(ch.Data))
		}
	}
	want := []string{"aa", "gap", "bb", "gap", "cc"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
}
