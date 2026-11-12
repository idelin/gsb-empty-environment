package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// TestConcurrentAttachOneWinner guarantees racing attachers never both obtain
// the data channel, regardless of whether data arrives before or after Attach.
func TestConcurrentAttachOneWinner(t *testing.T) {
	t.Run("attach before data", func(t *testing.T) {
		session, connector := mustSession(t, "henry")
		transfer, err := session.Authorize(passiveApproval("henry", goodPeer))
		if err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		bound, err := transfer.OpenPassive(context.Background())
		if err != nil {
			t.Fatalf("OpenPassive: %v", err)
		}
		listener, _ := connector.Listener(bound)

		const racers = 8
		var wg sync.WaitGroup
		results := make(chan error, racers)
		start := make(chan struct{})
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), waitBudget)
				defer cancel()
				conn, attachErr := transfer.Attach(ctx)
				if conn != nil {
					results <- nil
					return
				}
				results <- attachErr
			}()
		}
		close(start)

		legit := listener.Inject(goodPeer)
		wg.Wait()
		close(results)

		winners, refused := 0, 0
		for attachErr := range results {
			if attachErr == nil {
				winners++
			} else if errors.Is(attachErr, ErrTransferInUse) {
				refused++
			} else {
				t.Fatalf("unexpected attach error: %v", attachErr)
			}
		}
		if winners != 1 || refused != racers-1 {
			t.Fatalf("winners=%d refused=%d, want 1 and %d", winners, refused, racers-1)
		}

		// The same unique connection remains observable without a second grant.
		if transfer.Conn() == nil {
			t.Fatal("attached connection not observable after hand-off")
		}
		_ = legit
		_ = transfer.Complete()
	})

	t.Run("data before attach", func(t *testing.T) {
		session, connector := mustSession(t, "ivy")
		transfer, err := session.Authorize(passiveApproval("ivy", goodPeer))
		if err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		bound, err := transfer.OpenPassive(context.Background())
		if err != nil {
			t.Fatalf("OpenPassive: %v", err)
		}
		listener, _ := connector.Listener(bound)

		legit := listener.Inject(goodPeer)
		eventually(t, func() bool { return transfer.State() == Attached })

		ctx, cancel := context.WithTimeout(context.Background(), waitBudget)
		first, err := transfer.Attach(ctx)
		cancel()
		if err != nil || first == nil {
			t.Fatalf("first Attach: conn=%v err=%v", first, err)
		}

		ctx2, cancel2 := context.WithTimeout(context.Background(), waitBudget)
		defer cancel2()
		if _, err := transfer.Attach(ctx2); !errors.Is(err, ErrTransferInUse) {
			t.Fatalf("second Attach err = %v, want ErrTransferInUse", err)
		}
		_ = legit
		_ = transfer.Complete()
	})
}
