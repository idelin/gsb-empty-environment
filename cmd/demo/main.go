// Command demo exercises the lease lock: writer fairness under a reader
// stream, in-place upgrade, failed upgrade keeping the read lease, TTL
// revocation with stale-token rejection, and double-release rejection.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"lease"
)

func main() {
	ctx := context.Background()
	l := lease.New(
		lease.WithWriteTTL(300*time.Millisecond),
		lease.WithRevokeHook(func(gen uint64) error {
			fmt.Printf("[revoke] task ran, generation is now %d\n", gen)
			return nil
		}),
	)

	fmt.Println("== 1. writer under a continuous reader stream ==")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if tok, err := l.AcquireRead(ctx); err == nil {
					time.Sleep(2 * time.Millisecond)
					_ = l.Release(tok)
				}
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	wt, err := l.AcquireWrite(ctx)
	fmt.Println("writer acquired despite reader stream:", err == nil)
	_ = l.Release(wt)
	close(stop)
	wg.Wait()

	fmt.Println("== 2. sole reader upgrades in place ==")
	r1, _ := l.AcquireRead(ctx)
	wu, err := l.Upgrade(ctx, r1)
	fmt.Println("upgrade succeeded:", err == nil)
	_ = l.Release(wu)

	fmt.Println("== 3. failed upgrade keeps the read lease ==")
	hold, _ := l.AcquireRead(ctx)
	r2, _ := l.AcquireRead(ctx)
	uctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err = l.Upgrade(uctx, r2) // times out: "hold" keeps reading
	cancel()
	fmt.Println("upgrade failed as expected:", err)
	fmt.Println("original read lease still valid:", l.Release(r2) == nil)
	_ = l.Release(hold)

	fmt.Println("== 4. TTL revocation and stale token ==")
	wt2, _ := l.AcquireWrite(ctx)
	time.Sleep(400 * time.Millisecond) // exceeds the 300ms TTL
	fmt.Println("release of revoked token:", l.Release(wt2))

	fmt.Println("== 5. double release rejected ==")
	tok, _ := l.AcquireRead(ctx)
	_ = l.Release(tok)
	fmt.Println("second release:", l.Release(tok))
}
