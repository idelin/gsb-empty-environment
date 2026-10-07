package mailbox

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// TestQueueModel drives the queue with random operations and checks the
// visible order and ack/nack outcomes against a simple list-based
// reference model. It periodically reopens the queue from the same store
// to verify that recovery preserves the model exactly.
func TestQueueModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	clock := &manualClock{t: time.Unix(0, 0)}
	store := &MemoryStore{}
	const visTimeout = 10 * time.Second

	open := func() *Queue {
		q, err := New(store, Options{VisibilityTimeout: visTimeout, Now: clock.now})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return q
	}
	q := open()

	type mstate struct {
		body     string
		claimed  bool
		gen      uint64
		deadline time.Time
	}
	var modelOrder []string
	modelMsgs := map[string]*mstate{}
	tokens := map[string][]ClaimToken{} // every token ever issued per live message

	reopen := func() {
		q = open()
		if q.Len() != len(modelOrder) {
			t.Fatalf("after reopen: Len = %d, want %d", q.Len(), len(modelOrder))
		}
	}

	send := func(body string) {
		id, err := q.Send([]byte(body))
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		modelOrder = append(modelOrder, id)
		modelMsgs[id] = &mstate{body: body}
	}

	receive := func() {
		msg, tok, err := q.Receive()
		now := clock.t
		expID := ""
		for _, id := range modelOrder {
			ms := modelMsgs[id]
			if ms.claimed && now.Before(ms.deadline) {
				continue
			}
			expID = id
			break
		}
		if expID == "" {
			if err != ErrEmpty {
				t.Fatalf("Receive = %v, want ErrEmpty", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("Receive: %v", err)
		}
		ms := modelMsgs[expID]
		if msg.ID != expID || string(msg.Body) != ms.body {
			t.Fatalf("Receive = (%s, %q), want (%s, %q)", msg.ID, msg.Body, expID, ms.body)
		}
		ms.claimed = true
		ms.gen++
		ms.deadline = now.Add(visTimeout)
		if tok.Generation != ms.gen || msg.Generation != ms.gen {
			t.Fatalf("token generation = %d/%d, want %d", tok.Generation, msg.Generation, ms.gen)
		}
		tokens[expID] = append(tokens[expID], tok)
	}

	// wantAck mirrors the Queue's ack contract against the model.
	settle := func(nack bool) {
		if len(tokens) == 0 {
			return
		}
		ids := make([]string, 0, len(tokens))
		for id := range tokens {
			ids = append(ids, id)
		}
		id := ids[rng.Intn(len(ids))]
		hist := tokens[id]
		tok := hist[rng.Intn(len(hist))]

		var err error
		if nack {
			err = q.Nack(tok)
		} else {
			err = q.Ack(tok)
		}

		ms, live := modelMsgs[id]
		valid := live && ms.claimed && ms.gen == tok.Generation
		switch {
		case !live:
			// Message already acked: duplicate Ack is a no-op, Nack is stale.
			if nack && err != ErrStaleToken {
				t.Fatalf("Nack(%s gen %d) = %v, want ErrStaleToken", id, tok.Generation, err)
			}
			if !nack && err != nil {
				t.Fatalf("duplicate Ack(%s gen %d) = %v, want nil", id, tok.Generation, err)
			}
		case !valid:
			if err != ErrStaleToken {
				t.Fatalf("settle(%s gen %d) = %v, want ErrStaleToken", id, tok.Generation, err)
			}
		default:
			if err != nil {
				t.Fatalf("settle(%s gen %d): %v", id, tok.Generation, err)
			}
			if nack {
				ms.claimed = false
				ms.deadline = time.Time{}
				for i, x := range modelOrder {
					if x == id {
						modelOrder = append(modelOrder[:i], modelOrder[i+1:]...)
						break
					}
				}
				modelOrder = append(modelOrder, id)
			} else {
				delete(modelMsgs, id)
				delete(tokens, id)
				for i, x := range modelOrder {
					if x == id {
						modelOrder = append(modelOrder[:i], modelOrder[i+1:]...)
						break
					}
				}
			}
		}
	}

	for i := 0; i < 3000; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2:
			send(fmt.Sprintf("body-%d", i))
		case 3, 4, 5:
			receive()
		case 6, 7:
			settle(false)
		case 8:
			settle(true)
		case 9:
			if rng.Intn(2) == 0 {
				clock.advance(time.Duration(rng.Intn(30)) * time.Second)
			} else {
				reopen()
			}
		}
		if q.Len() != len(modelOrder) {
			t.Fatalf("iter %d: Len = %d, want %d", i, q.Len(), len(modelOrder))
		}
	}

	// Drain: everything left must come out in model order.
	clock.advance(time.Hour)
	for len(modelOrder) > 0 {
		msg, tok, err := q.Receive()
		if err != nil {
			t.Fatalf("drain Receive: %v", err)
		}
		want := modelOrder[0]
		if msg.ID != want || string(msg.Body) != modelMsgs[want].body {
			t.Fatalf("drain got (%s, %q), want (%s, %q)", msg.ID, msg.Body, want, modelMsgs[want].body)
		}
		if err := q.Ack(tok); err != nil {
			t.Fatalf("drain Ack: %v", err)
		}
		modelOrder = modelOrder[1:]
		delete(modelMsgs, want)
	}
	if _, _, err := q.Receive(); err != ErrEmpty {
		t.Fatalf("final Receive = %v, want ErrEmpty", err)
	}
}
