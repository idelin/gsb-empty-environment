package handshake

import (
	"fmt"
	"testing"
)

func BenchmarkSolveWithEdges18(b *testing.B) {
	messages := make([]Message, MaxMessages)
	for i := range messages {
		messages[i] = Message{ID: fmt.Sprintf("m%02d", i), Kind: KindSend}
	}
	// A chain leaves a unique topological order while still exercising every
	// predecessor check in the bitmask dynamic program.
	edges := make([]Edge, 0, MaxMessages-1)
	for i := 1; i < MaxMessages; i++ {
		edges = append(edges, Edge{
			From:   messages[i-1].ID,
			To:     messages[i].ID,
			Reason: EdgeLocalOrder,
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := SolveWithEdges(messages, edges); err != nil {
			b.Fatal(err)
		}
	}
}
