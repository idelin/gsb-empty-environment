package netsec

import "testing"

func TestSpecAnswerBudget(t *testing.T) {
	spec := NewSpec(2)
	create := Operation{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "code"}
	if !spec.Apply(create) {
		t.Fatal("create should apply")
	}

	wrong1 := Operation{ID: 2, Kind: Answer, ChallengeID: "c1", Answer: "nope", Result: ErrWrongAnswer}
	if !spec.Apply(wrong1) {
		t.Fatal("first wrong answer should apply")
	}
	wrong2 := Operation{ID: 3, Kind: Answer, ChallengeID: "c1", Answer: "nope", Result: ErrLocked}
	if !spec.Apply(wrong2) {
		t.Fatal("second wrong answer should lock the challenge")
	}
	locked := Operation{ID: 4, Kind: Answer, ChallengeID: "c1", Answer: "code", Result: ErrLocked}
	if !spec.Apply(locked) {
		t.Fatal("locked challenge rejects even the correct code")
	}
	if spec.Apply(Operation{ID: 5, Kind: Grant, ChallengeID: "c1", Result: OK}) {
		t.Fatal("locked challenge cannot authorize")
	}
}

func TestSpecGrantIsSingleUseAndCurrentOnly(t *testing.T) {
	spec := NewSpec(DefaultWrongLimit)
	spec.Apply(Operation{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a"})
	spec.Apply(Operation{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "b"})

	if !spec.Apply(Operation{ID: 3, Kind: Grant, ChallengeID: "c1", Result: ErrExpired}) {
		t.Fatal("c1 is no longer current and must be expired")
	}
	if !spec.Apply(Operation{ID: 4, Kind: Grant, ChallengeID: "c2", Result: OK}) {
		t.Fatal("current challenge authorizes once")
	}
	if !spec.Apply(Operation{ID: 5, Kind: Grant, ChallengeID: "c2", Result: ErrConsumed}) {
		t.Fatal("second grant on the same challenge is consumed/replay")
	}
}

func TestSpecReplaceAndReplay(t *testing.T) {
	spec := NewSpec(DefaultWrongLimit)
	spec.Apply(Operation{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a"})

	if !spec.Apply(Operation{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "b"}) {
		t.Fatal("current challenge can be replaced")
	}
	if !spec.Apply(Operation{ID: 3, Kind: Replace, ChallengeID: "c1", ReplacementID: "c3", Result: ErrExpired}) {
		t.Fatal("stale challenge cannot replace the current one")
	}
	if !spec.Apply(Operation{ID: 4, Kind: Create, ChallengeID: "c1", Result: ErrReplay}) {
		t.Fatal("identifier reuse is rejected")
	}
	if !spec.Apply(Operation{ID: 5, Kind: Consume, ChallengeID: "ghost", Result: ErrNoChallenge}) {
		t.Fatal("unknown challenge yields no_challenge")
	}
}

func TestSpecConsumeThenGrant(t *testing.T) {
	spec := NewSpec(DefaultWrongLimit)
	spec.Apply(Operation{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "a"})
	if !spec.Apply(Operation{ID: 2, Kind: Consume, ChallengeID: "c1", Result: OK}) {
		t.Fatal("consume succeeds on active challenge")
	}
	if !spec.Apply(Operation{ID: 3, Kind: Grant, ChallengeID: "c1", Result: ErrConsumed}) {
		t.Fatal("consumed challenge cannot authorize")
	}
	if !spec.Apply(Operation{ID: 4, Kind: Answer, ChallengeID: "c1", Answer: "a", Result: ErrConsumed}) {
		t.Fatal("consumed challenge rejects further answers")
	}
}
