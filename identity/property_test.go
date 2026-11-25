package identity

import (
	"math/rand"
	"testing"
)

func TestEventLimit36(t *testing.T) {
	events := make([]Event, 0, 37)
	for i := 0; i < 36; i++ {
		events = append(events, Event{
			ID:     i + 1,
			Chain:  i,
			Thread: 1,
			Kind:   EnterRequest,
			Token:  tok("a", 1),
		})
	}
	if _, err := Analyze(Input{Events: events}, Budget{}); err != nil {
		t.Fatalf("36 events must be accepted: %v", err)
	}
	events = append(events, Event{ID: 37, Chain: 36, Thread: 1, Kind: EnterRequest, Token: tok("a", 1)})
	if _, err := Analyze(Input{Events: events}, Budget{}); err != errTooManyEvents {
		t.Fatalf("37 events must be rejected, got %v", err)
	}
}

// 随机小事件集：状态机分析的可达性/分类必须与全排列 oracle 完全一致。
func TestRandomizedAgreesWithPermutationOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	for seed := 0; seed < 15; seed++ {
		in := randomInput(rng)
		res, err := Analyze(in, Budget{})
		if err != nil {
			t.Fatalf("seed %d: analyze failed: %v", seed, err)
		}
		if !res.Exhaustive {
			t.Fatalf("seed %d: small input must be exhaustive within default budget", seed)
		}
		assertOracleAgrees(t, in, res)
	}
}

func randomInput(rng *rand.Rand) Input {
	chains := 1 + rng.Intn(2)
	events := []Event{}
	id := 0
	nextBorrow := 0
	type chainCursor struct{ events []int }
	cursors := make([]chainCursor, chains)

	for c := 0; c < chains; c++ {
		length := 2 + rng.Intn(3)
		hasEnter := false
		for k := 0; k < length; k++ {
			id++
			ev := Event{ID: id, Chain: c, Thread: c + 1}
			if !hasEnter {
				ev.Kind = EnterRequest
				ev.Token = tok("a", int64(1+rng.Intn(2)))
				hasEnter = true
			} else {
				switch rng.Intn(5) {
				case 0:
					ev.Kind = Borrow
					nextBorrow++
					ev.BorrowID = nextBorrow
					ev.Token = tok("b", 1)
				case 1:
					ev.Kind = ProtectedRead
					ev.Policy = pol("a", 1)
				case 2:
					ev.Kind = Spawn
					ev.Slot = rng.Intn(2)
					ev.HandleGeneration = int64(1 + rng.Intn(2))
				case 3:
					ev.Kind = Suspend
				default:
					ev.Kind = Restore
					ev.BorrowID = nextBorrow
				}
			}
			cursors[c].events = append(cursors[c].events, id)
			events = append(events, ev)
		}
	}
	return Input{Events: events}
}

func TestWidePartialOrderBounded(t *testing.T) {
	events := make([]Event, 0, 36)
	for c := 0; c < 3; c++ {
		events = append(events, Event{
			ID: c + 1, Chain: c, Thread: c + 1, Kind: EnterRequest,
			Token: tok("acme", 1),
		})
		events = append(events, Event{
			ID: c + 7, Chain: c, Thread: c + 1, Kind: Borrow,
			Token: tok("partner", 1), BorrowID: c + 100,
		})
		events = append(events, Event{
			ID: c + 13, Chain: c, Thread: c + 1, Kind: Spawn,
			Slot: c, HandleGeneration: 1,
		})
		events = append(events, Event{
			ID: c + 19, Chain: c, Thread: c + 1, Kind: Restore, BorrowID: c + 100,
		})
		events = append(events, Event{
			ID: c + 25, Chain: c, Thread: c + 1, Kind: ProtectedRead,
			Policy: pol("acme", 1),
		})
		events = append(events, Event{
			ID: c + 31, Chain: c, Thread: c + 1, Kind: Exit,
		})
	}
	res, err := Analyze(Input{Events: events}, Budget{MaxStates: 5000, MaxTransitions: 50000})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatesExplored > 5000 {
		t.Fatalf("budget not honored: %d states", res.StatesExplored)
	}
	if !res.HasLegalHistory {
		t.Fatal("balanced chains must admit a legal history")
	}
	for c := 0; c < 3; c++ {
		readID := c + 25
		rr := res.Reads[readID]
		if rr == nil {
			t.Fatalf("missing result for read %d", readID)
		}
	}
}
