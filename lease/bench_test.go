package lease

import (
	"context"
	"testing"
	"time"
)

// Measures handover switchover latency under concurrent write load.
func BenchmarkHandoverSwitchover(b *testing.B) {
	coord := NewCoordinator(nil)
	a := NewNode("A", coord)
	candidate := NewNode("B", coord)
	router := NewRouter(a, candidate)
	if err := a.Acquire("orders", time.Hour); err != nil {
		b.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			_ = router.Write(ctx, "order-event")
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.InitiateHandover(candidate); err != nil {
			b.Fatal(err)
		}
		a, candidate = candidate, a
	}
}
