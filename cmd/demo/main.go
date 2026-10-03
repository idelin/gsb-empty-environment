// Command demo runs two delta-CRDT nodes over a synchronous net.Conn
// pair: a 10 000-character document is synced in full once, then a
// single character is modified and re-synced with a constant-size
// delta. The wire protocol (length-prefixed gob envelopes) is exactly
// what SyncConn speaks over TCP; swap net.Pipe for a TCP dial/listen
// pair to run across machines.
package main

import (
	"fmt"
	"log"
	"net"

	"deltacrdt/delta"
)

const docSize = 10000

// syncRound runs one protocol round on both ends of the connection
// pair concurrently (net.Pipe is synchronous).
func syncRound(a, b *delta.Node, c1, c2 net.Conn) {
	errCh := make(chan error, 2)
	go func() { errCh <- delta.SyncConn(a, b.ID(), c1, 1) }()
	go func() { errCh <- delta.SyncConn(b, a.ID(), c2, 1) }()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			log.Fatal(err)
		}
	}
}

func main() {
	a, b := delta.NewNode("A"), delta.NewNode("B")

	// Node A authors a 10 000-character document.
	parent := delta.Dot{}
	for i := 0; i < docSize; i++ {
		parent = a.Doc().InsertAfter(parent, 'a'+rune(i%26))
	}

	// Connect both nodes. net.Pipe is a synchronous in-memory
	// net.Conn; a TCP connection works identically.
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	fmt.Printf("document size: %d characters (%d ops)\n", docSize, a.Doc().OpCount())

	// Round 1: B knows nothing -> full-state transfer.
	fullSize, _ := delta.EncodedSize(a.DeltaFor(b.ID()))
	fmt.Printf("round 1 (initial sync):  %8d bytes  (full state, %d ops)\n",
		fullSize, len(a.DeltaFor(b.ID()).Ops))
	syncRound(a, b, c1, c2)

	// A modifies exactly one character in the middle (delete + insert).
	before, _ := a.Doc().DotAt(docSize/2 - 1)
	doomed, _ := a.Doc().DotAt(docSize / 2)
	a.Doc().Delete(doomed)
	a.Doc().InsertAfter(before, 'Z')

	// Round 2: B already acked everything -> incremental delta only.
	deltaSize, _ := delta.EncodedSize(a.DeltaFor(b.ID()))
	fmt.Printf("round 2 (1 char change): %8d bytes  (incremental delta, %d ops)\n",
		deltaSize, len(a.DeltaFor(b.ID()).Ops))
	syncRound(a, b, c1, c2)

	// Verify: B's delta-synced state must equal a full-state transfer.
	c := delta.NewNode("C")
	c.HandleDelta(a.ID(), a.DeltaFor(c.ID()))
	fmt.Printf("B converged to A:                %v\n", a.Doc().EqualState(b.Doc()))
	fmt.Printf("B equals full-state transfer:    %v\n", b.Doc().EqualState(c.Doc()))
	fmt.Printf("compression ratio:               %.0fx\n", float64(fullSize)/float64(deltaSize))
}
