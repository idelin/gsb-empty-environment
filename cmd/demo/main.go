// Demo: out-of-order, overlapping fragments over an arbitrarily chunked
// byte stream, plus conflict, quota and expiry behavior.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"reassemble"
)

func main() {
	now := time.Now()
	clock := func() time.Time { return now }
	h := reassemble.NewHandler(reassemble.Config{
		MaxMessageSize:   1024,
		MaxBufferedBytes: 4096,
		TTL:              30 * time.Second,
		CompletionTTL:    time.Minute,
	}, clock)

	msg := []byte("the quick brown fox jumps over the lazy dog")
	total := uint32(len(msg))

	// Fragments: out of order, with consistent overlaps and a duplicate.
	var stream []byte
	stream = reassemble.AppendFrame(stream, 7, total, 20, msg[20:35])
	stream = reassemble.AppendFrame(stream, 7, total, 0, msg[0:16])
	stream = reassemble.AppendFrame(stream, 7, total, 10, msg[10:30]) // overlaps both
	stream = reassemble.AppendFrame(stream, 7, total, 30, msg[30:])
	stream = reassemble.AppendFrame(stream, 7, total, 0, msg[0:16]) // duplicate

	// Shuffle the byte stream into random small chunks (1..9 bytes).
	rng := rand.New(rand.NewSource(42))
	var done [][]byte
	for len(stream) > 0 {
		n := 1 + rng.Intn(9)
		if n > len(stream) {
			n = len(stream)
		}
		d, err := h.Write(stream[:n])
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		done = append(done, d...)
		stream = stream[n:]
	}
	fmt.Printf("1) reassembled %d message(s): %q\n", len(done), done[0])

	// 2) Conflict: same range, one byte different -> rejected, never "picked".
	r := h.Reasm
	if _, _, err := r.Add(8, 6, 0, []byte("abc")); err != nil {
		fmt.Println("error:", err)
	}
	_, _, err := r.Add(8, 6, 0, []byte("abd"))
	fmt.Printf("2) one-byte overlap conflict: %v\n", err)

	// 3) Quota: checked before allocation.
	_, _, err = r.Add(9, 5000, 0, []byte("x"))
	fmt.Printf("3) per-message quota: %v\n", err)

	// 4) Completion dedup: re-sent fragments of a finished message.
	data, complete, _ := r.Add(7, int(total), 0, msg[0:16])
	fmt.Printf("4) duplicate of completed message: complete=%v data=%v (deduped)\n", complete, data)

	// 5) Expiry: a late fragment must not borrow the stale buffer.
	if _, _, err := r.Add(10, 10, 0, []byte("hello")); err != nil {
		fmt.Println("error:", err)
	}
	now = now.Add(time.Minute) // TTL (30s) elapses
	_, complete, _ = r.Add(10, 10, 5, []byte("world"))
	fmt.Printf("5) late fragment after expiry completed from stale buffer: %v (want false)\n", complete)
	data, complete, _ = r.Add(10, 10, 0, []byte("HELLO"))
	fmt.Printf("   fresh message completes: %v -> %q\n", complete, data)
}
