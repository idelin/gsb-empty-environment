package netsec

import (
	"bytes"
	"errors"
	"math/rand"
	"sort"
	"testing"
)

var testKey = []byte("unit-test-hmac-key")

func rec(id, user, group string, window int64, payload string) Record {
	return Record{
		EventID: id,
		UserID:  user,
		Window:  window,
		Group:   group,
		Payload: []byte(payload),
	}
}

func idsOf(rs []Record) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.EventID
	}
	return out
}

func reasonMap(dropped []DroppedRecord) map[string]Reason {
	out := make(map[string]Reason, len(dropped))
	for _, d := range dropped {
		out[d.Record.EventID] = d.Reason
	}
	return out
}

func assertIDs(t *testing.T, label string, got []Record, want []string) {
	t.Helper()
	gotIDs := idsOf(got)
	if len(gotIDs) != len(want) {
		t.Fatalf("%s: got %v, want %v", label, gotIDs, want)
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", label, gotIDs, want)
		}
	}
}

func TestNewSelectorValidation(t *testing.T) {
	if _, err := NewSelector(nil, 1, 1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty key: got %v", err)
	}
	if _, err := NewSelector(testKey, 0, 1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero local cap: got %v", err)
	}
	if _, err := NewSelector(testKey, 1, -2); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative global cap: got %v", err)
	}
}

// TestTrimCapsAndReasons verifies the two-level cap, deterministic digest
// ordering and the reported trimming reasons.
func TestTrimCapsAndReasons(t *testing.T) {
	const localK, globalCap = 2, 3

	records := []Record{
		rec("a1", "u1", "gA", 7, "p1"),
		rec("a2", "u1", "gA", 7, "p2"),
		rec("a3", "u1", "gA", 7, "p3"),
		rec("b1", "u1", "gB", 7, "p4"),
		rec("b2", "u1", "gB", 7, "p5"),
		rec("b3", "u1", "gB", 7, "p6"),
	}

	type ranked struct {
		id     string
		digest [digestSize]byte
	}
	all := make([]ranked, 0, len(records))
	for _, r := range records {
		all = append(all, ranked{id: r.EventID, digest: KeyedDigest(testKey, r)})
	}
	sort.Slice(all, func(i, j int) bool {
		if c := bytes.Compare(all[i].digest[:], all[j].digest[:]); c != 0 {
			return c < 0
		}
		return all[i].id < all[j].id
	})

	byGroup := map[string][]ranked{}
	for _, x := range all {
		byGroup[x.id[:1]] = append(byGroup[x.id[:1]], x)
	}

	expectedLocalLosers := map[string]bool{}
	expectedSurvivors := map[string]bool{}
	for _, group := range []string{"a", "b"} {
		for i, x := range byGroup[group] {
			if i < localK {
				expectedSurvivors[x.id] = true
			} else {
				expectedLocalLosers[x.id] = true
			}
		}
	}

	var expectedKept, expectedGlobalLosers []string
	survivorsRanked := make([]ranked, 0, len(expectedSurvivors))
	for _, x := range all {
		if expectedSurvivors[x.id] {
			survivorsRanked = append(survivorsRanked, x)
		}
	}
	for i, x := range survivorsRanked {
		if i < globalCap {
			expectedKept = append(expectedKept, x.id)
		} else {
			expectedGlobalLosers = append(expectedGlobalLosers, x.id)
		}
	}

	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 5; iter++ {
		s, err := NewSelector(testKey, localK, globalCap)
		if err != nil {
			t.Fatalf("NewSelector: %v", err)
		}

		for _, idx := range rng.Perm(len(records)) {
			if err := s.Ingest(records[idx]); err != nil {
				t.Fatalf("Ingest %s: %v", records[idx].EventID, err)
			}
		}

		res := s.Trim()
		assertIDs(t, "kept", res.Kept, expectedKept)

		reasons := reasonMap(res.Dropped)
		if len(reasons) != len(expectedLocalLosers)+len(expectedGlobalLosers) {
			t.Fatalf("dropped count: got %d entries %v", len(reasons), reasons)
		}
		for id := range expectedLocalLosers {
			if reasons[id] != ReasonLocalCap {
				t.Fatalf("%s: reason = %v, want local_cap", id, reasons[id])
			}
		}
		for _, id := range expectedGlobalLosers {
			if reasons[id] != ReasonGlobalCap {
				t.Fatalf("%s: reason = %v, want global_cap", id, reasons[id])
			}
		}

		for i := 1; i < len(res.Kept); i++ {
			prev := KeyedDigest(testKey, res.Kept[i-1])
			cur := KeyedDigest(testKey, res.Kept[i])
			if bytes.Compare(prev[:], cur[:]) > 0 {
				t.Fatalf("kept not digest-ordered at %d", i)
			}
		}
	}

	s, _ := NewSelector(testKey, localK, globalCap)
	for _, r := range records {
		if err := s.Ingest(r); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}
	res := s.Trim()
	if len(res.Kept)+len(res.Dropped) != len(records) {
		t.Fatalf("accounting: kept=%d dropped=%d total=%d", len(res.Kept), len(res.Dropped), len(records))
	}
}
