// Demo of the ordered dirty-queue write-back cache.
//
// Phase 1: the backing source fails twice (injected failure); the cache
// retries with order preserved and Close drains the queue.
// Phase 2: the backing source always fails; Close times out and returns
// the unflushed ops explicitly.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"writeback"
)

// source is a fake backing source with injectable failures.
type source struct {
	mu        sync.Mutex
	failTimes int
	state     map[string]string
	calls     int
}

func (s *source) sink(_ context.Context, batch []writeback.Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failTimes > 0 {
		s.failTimes--
		fmt.Printf("  sink call #%d: batch of %d -> INJECTED FAILURE (dirty kept, will retry)\n", s.calls, len(batch))
		return errors.New("source unavailable")
	}
	for _, op := range batch {
		if op.Kind == writeback.OpDelete {
			delete(s.state, op.Key)
		} else {
			s.state[op.Key] = string(op.Value)
		}
	}
	fmt.Printf("  sink call #%d: flushed batch %s\n", s.calls, formatBatch(batch))
	return nil
}

func formatBatch(batch []writeback.Op) string {
	out := "["
	for i, op := range batch {
		if i > 0 {
			out += " "
		}
		if op.Kind == writeback.OpDelete {
			out += fmt.Sprintf("#%d DELETE %s", op.Seq, op.Key)
		} else {
			out += fmt.Sprintf("#%d SET %s=%s", op.Seq, op.Key, op.Value)
		}
	}
	return out + "]"
}

func main() {
	fmt.Println("=== phase 1: failures, ordered retry, drain on close ===")
	src := &source{failTimes: 2, state: map[string]string{}}
	c := writeback.New(writeback.Options{
		Sink:          src.sink,
		BatchSize:     3,
		FlushInterval: 20 * time.Millisecond,
		RetryBackoff:  30 * time.Millisecond,
	})

	must(c.Set("alpha", []byte("1")))
	must(c.Set("beta", []byte("2")))
	must(c.Set("alpha", []byte("3"))) // merges into alpha's slot
	must(c.Delete("beta"))            // merges into beta's slot
	must(c.Set("gamma", []byte("4")))
	fmt.Printf("buffered: dirty=%d watermark=%d\n", c.DirtyLen(), c.Watermark())

	time.Sleep(150 * time.Millisecond) // let the background flusher retry
	fmt.Printf("after retries: dirty=%d watermark=%d source=%v\n", c.DirtyLen(), c.Watermark(), src.state)

	unflushed, err := c.Close(context.Background())
	fmt.Printf("close: err=%v unflushed=%v watermark=%d source=%v\n", err, formatBatch(unflushed), c.Watermark(), src.state)

	fmt.Println("=== phase 2: source down, close returns the unflushed list ===")
	down := &source{failTimes: 1 << 30, state: map[string]string{}}
	c2 := writeback.New(writeback.Options{
		Sink:          down.sink,
		FlushInterval: 10 * time.Millisecond,
		RetryBackoff:  20 * time.Millisecond,
	})
	must(c2.Set("x", []byte("1")))
	must(c2.Set("y", []byte("2")))
	must(c2.Delete("z"))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	left, err := c2.Close(ctx)
	fmt.Printf("close: err=%v\n", err)
	fmt.Printf("unflushed (still dirty, in order): %s\n", formatBatch(left))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
