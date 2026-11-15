package netsec_test

import (
	"fmt"
	"time"

	netsec "example.org/netsec/q-115"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

func ExampleEngine() {
	clock := fixedClock{now: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)}
	engine, err := netsec.New(netsec.NewMemoryStore(), netsec.WithClock(clock))
	if err != nil {
		panic(err)
	}

	const roundID = "example-round"
	if _, _, err := engine.StartRound(netsec.RoundConfig{
		RoundID:       roundID,
		Participants:  []string{"alice"},
		CommitTimeout: time.Minute,
		RevealTimeout: time.Minute,
	}); err != nil {
		panic(err)
	}

	var nonce netsec.Nonce
	for i := range nonce {
		nonce[i] = byte(i)
	}
	commitment := netsec.CommitmentFor(roundID, "alice", nonce)
	if _, err := engine.Commit(roundID, "alice", commitment); err != nil {
		panic(err)
	}
	if _, err := engine.Reveal(roundID, "alice", nonce); err != nil {
		panic(err)
	}

	view, err := engine.Round(roundID)
	if err != nil {
		panic(err)
	}
	fmt.Println(view.Phase, view.HasResult, view.RevealCount)
	// Output: finalized true 1
}
