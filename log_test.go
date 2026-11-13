package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestLogAppendIsContiguousAndCopied(t *testing.T) {
	log := NewLog()
	payload := []byte("a")
	for want := uint64(1); want <= 5; want++ {
		ev := log.Append(payload)
		if ev.Seq != want {
			t.Fatalf("append seq = %d, want %d", ev.Seq, want)
		}
		payload[0]++
	}
	bounds := log.Bounds()
	if bounds.Earliest != 1 || bounds.Newest != 5 {
		t.Fatalf("bounds = %+v, want {1 5}", bounds)
	}
	got, err := log.Read(1, 5, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("read %d events, want 5", len(got))
	}
	// Every stored payload must be the caller's bytes at append time.
	for i, ev := range got {
		if string(ev.Payload) != string(rune('a'+i)) {
			t.Fatalf("payload[%d] = %q, want %q", i, ev.Payload, string(rune('a'+i)))
		}
	}
}

func TestLogReadRangesAndWindowRoll(t *testing.T) {
	log := NewLog()
	for i := 0; i < 10; i++ {
		log.Append([]byte("x"))
	}
	log.TrimThrough(4)

	bounds := log.Bounds()
	if bounds.Earliest != 5 || bounds.Newest != 10 {
		t.Fatalf("bounds after trim = %+v, want {5 10}", bounds)
	}
	if _, err := log.Read(4, 10, 16); !errors.Is(err, ErrWindowRolled) {
		t.Fatalf("read before window err = %v, want ErrWindowRolled", err)
	}
	got, err := log.Read(5, 7, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Seq != 5 || got[2].Seq != 7 {
		t.Fatalf("range read = %+v, want seq 5..7", got)
	}
	small, err := log.Read(5, 10, 2)
	if err != nil || len(small) != 2 || small[1].Seq != 6 {
		t.Fatalf("capped read = %+v err=%v, want 2 events seq 5,6", small, err)
	}
}

// TestLiveCursorHandoffUnderConcurrentWrites is the core continuity
// guarantee: a tail subscription opened in the middle of concurrent appends
// must see every sequence exactly once, in order.
func TestLiveCursorHandoffUnderConcurrentWrites(t *testing.T) {
	log := NewLog()
	for i := 0; i < 50; i++ {
		log.Append(nil)
	}

	live := log.OpenCursor(25)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Writer keeps appending while the subscription drains.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			log.Append(nil)
		}
	}()

	seen := make([]uint64, 0, 226)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			ev, err := live.Next(ctx)
			if err != nil {
				return
			}
			seen = append(seen, ev.Seq)
			if ev.Seq == 250 {
				cancel()
				return
			}
		}
	}()
	wg.Wait()
	live.Cancel()

	if len(seen) != 226 {
		t.Fatalf("received %d events, want 226 (seq 25..250)", len(seen))
	}
	for i, seq := range seen {
		if seq != uint64(25+i) {
			t.Fatalf("discontinuity at index %d: got seq %d, want %d", i, seq, 25+i)
		}
	}
}

func TestLiveCursorFailsAfterTrimRollsWindow(t *testing.T) {
	log := NewLog()
	for i := 0; i < 10; i++ {
		log.Append(nil)
	}
	live := log.OpenCursor(8)

	// Consume 8 and 9; seq 10 stays queued and is still needed.
	ctx := context.Background()
	for _, want := range []uint64{8, 9} {
		ev, err := live.Next(ctx)
		if err != nil || ev.Seq != want {
			t.Fatalf("Next = %+v, %v; want seq %d", ev, err, want)
		}
	}
	log.TrimThrough(10)
	if _, err := live.Next(ctx); !errors.Is(err, ErrWindowRolled) {
		t.Fatalf("Next after window roll = %v, want ErrWindowRolled", err)
	}
}
