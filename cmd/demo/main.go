// Command demo simulates a chunked byte stream of requests and responses
// and shows how the correlator pairs them: latency, duplicates,
// out-of-order delivery, timeouts, orphans, epoch isolation, sequence
// reuse, and checkpoint/restore.
package main

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"time"

	"correlator"
)

func req(epoch, seq uint64, payload string) correlator.Message {
	return correlator.Message{Kind: correlator.KindRequest, Epoch: epoch, Seq: seq, Payload: []byte(payload)}
}

func resp(epoch, seq uint64, payload string) correlator.Message {
	return correlator.Message{Kind: correlator.KindResponse, Epoch: epoch, Seq: seq, Payload: []byte(payload)}
}

func main() {
	c := correlator.New(correlator.Config{
		Timeout:       300 * time.Millisecond,
		MaxPending:    16,
		MaxTombstones: 8,
	})
	p := correlator.NewParser(4096, 1<<20)
	rng := rand.New(rand.NewSource(7)) // deterministic chunking

	// send encodes messages into one stream, feeds it to the parser in
	// random-size chunks (frames split at arbitrary byte boundaries),
	// and dispatches every decoded message to the correlator.
	send := func(msgs ...correlator.Message) {
		var stream []byte
		for _, m := range msgs {
			stream = append(stream, m.Marshal()...)
		}
		for off := 0; off < len(stream); {
			n := 1 + rng.Intn(23)
			if off+n > len(stream) {
				n = len(stream) - off
			}
			if err := p.Feed(stream[off : off+n]); err != nil {
				log.Fatalf("feed: %v", err)
			}
			off += n
		}
		for {
			m, err := p.Next()
			if errors.Is(err, correlator.ErrIncomplete) {
				break
			}
			if err != nil {
				log.Fatalf("corrupt stream: %v", err)
			}
			fmt.Println("   ", c.HandleMessage(m))
		}
	}
	expire := func() {
		for _, ev := range c.Expire() {
			fmt.Println("   ", ev)
		}
	}

	fmt.Println("== 1. normal + out-of-order responses (epoch 1, seq 1..3)")
	send(req(1, 1, "a"), req(1, 2, "b"), req(1, 3, "c"))
	time.Sleep(50 * time.Millisecond)
	send(resp(1, 2, "B"), resp(1, 3, "C"), resp(1, 1, "A"))

	fmt.Println("== 2. replayed duplicate request keeps original start time")
	send(req(1, 4, "d"))
	time.Sleep(150 * time.Millisecond)
	send(req(1, 4, "d")) // retransmission
	time.Sleep(50 * time.Millisecond)
	send(resp(1, 4, "D")) // latency counts from the first request

	fmt.Println("== 3. orphan responses (unknown key / old epoch)")
	send(resp(9, 9, "?"))     // never requested
	send(resp(0, 1, "stale")) // epoch 0 straggler; must not match anything

	fmt.Println("== 4. timeout, then the response arrives late")
	send(req(1, 5, "e"))
	time.Sleep(350 * time.Millisecond) // beyond the 300ms timeout
	expire()
	send(resp(1, 5, "E")) // late, not a success

	fmt.Println("== 5. sequence number reuse after completion")
	send(req(1, 1, "a2")) // seq 1 was completed in phase 1
	time.Sleep(40 * time.Millisecond)
	send(resp(1, 1, "A2"))

	fmt.Println("== 6. checkpoint and restore (pending request survives)")
	send(req(1, 6, "f"))
	data, err := c.Snapshot()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("    checkpoint:", string(data))

	restored := correlator.New(correlator.Config{Timeout: 300 * time.Millisecond, MaxPending: 16, MaxTombstones: 8})
	if err := restored.Restore(data); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("    restored correlator: pending=%d\n", restored.Pending())

	time.Sleep(80 * time.Millisecond)
	fmt.Println("    response after restart (latency spans the checkpoint):")
	fmt.Println("   ", restored.HandleResponse(1, 6))
	fmt.Println("    late response for the expired request is still late:")
	fmt.Println("   ", restored.HandleResponse(1, 5))
}
