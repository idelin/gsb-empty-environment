// Demo of the writeback cache: ordered dirty queue, batched background
// flush, injected failure with in-order retry, same-key merge, and a
// draining Close. Everything runs against an in-memory source.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"writeback"
)

// loggingSource is an in-memory "remote" with an injectable failure budget.
type loggingSource struct {
	mu       sync.Mutex
	data     map[string]string
	failNext int // next N ops fail, simulating an outage
}

func (s *loggingSource) Source() writeback.Source {
	return writeback.Source{
		Store: func(_ context.Context, k string, v []byte) error {
			return s.apply("STORE", k, string(v))
		},
		Delete: func(_ context.Context, k string) error {
			return s.apply("DELETE", k, "")
		},
	}
}

func (s *loggingSource) apply(op, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext > 0 {
		s.failNext--
		fmt.Printf("  source: %-6s %-6s -> INJECTED FAILURE\n", op, k)
		return fmt.Errorf("injected failure on %s %s", op, k)
	}
	if op == "STORE" {
		s.data[k] = v
	} else {
		delete(s.data, k)
	}
	fmt.Printf("  source: %-6s %-6s -> ok (value=%q)\n", op, k, v)
	return nil
}

func main() {
	src := &loggingSource{data: map[string]string{}}

	c := writeback.New(writeback.Options{
		Source:        src.Source(),
		Now:           time.Now,
		FlushInterval: 0, // manual flush for a deterministic demo
		MaxBatch:      8,
		Backoff:       func(attempt int) time.Duration { return 10 * time.Millisecond },
	})

	fmt.Println("== writes enter the ordered dirty queue ==")
	c.Set("alice", []byte("v1"))
	c.Set("bob", []byte("v1"))
	c.Set("alice", []byte("v2")) // merges into alice's slot
	c.Delete("bob")              // merges into bob's slot, keeps position
	c.Set("carol", []byte("v1"))
	fmt.Printf("pending keys: %v (alice/bob merged in place)\n", c.Unflushed())

	fmt.Println("\n== first flush: inject 1 failure, order is preserved ==")
	src.failNext = 1 // the very first STORE fails
	if n, err := c.Flush(context.Background()); err != nil {
		fmt.Printf("flush committed %d entr(ies) then failed: %v\n", n, err)
	}
	fmt.Printf("still dirty: %v, watermark=%d\n", c.Unflushed(), c.Watermark())

	fmt.Println("\n== retry: failed entries keep dirty flag, retried in order ==")
	if n, err := c.Flush(context.Background()); err == nil {
		fmt.Printf("retry committed %d entr(ies), watermark=%d\n", n, c.Watermark())
	}

	fmt.Println("\n== Close drains the queue even through fresh failures ==")
	c.Set("dave", []byte("v1"))
	c.Delete("alice")
	src.failNext = 2 // outage during Close
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		fmt.Printf("close: %v, unflushed: %v\n", err, c.Unflushed())
	} else {
		fmt.Printf("close: drained, watermark=%d, pending=%d\n", c.Watermark(), c.Pending())
	}

	fmt.Println("\n== Shutdown returns the unflushed list without retrying ==")
	c2 := writeback.New(writeback.Options{Source: src.Source(), FlushInterval: 0})
	src.failNext = 100 // permanent outage
	c2.Set("x", []byte("1"))
	c2.Delete("y")
	c2.Set("z", []byte("3"))
	fmt.Printf("shutdown unflushed: %v\n", c2.Shutdown())

	fmt.Printf("\nfinal source contents: %v\n", src.data)
}
