package netsec_test

import (
	"reflect"
	"testing"

	"example.org/netsec/q0019"
)

func TestSearchFindsAliasedAttackInEveryVersion(t *testing.T) {
	for _, version := range []netsec.Version{netsec.VersionA, netsec.VersionB, netsec.VersionC} {
		var mem netsec.Memory
		result := netsec.Search(netsec.SearchRequest{
			Memory:      mem,
			Header:      0,
			PrevAllowed: netsec.ValueSet{4},
			NextAllowed: netsec.ValueSet{5},
			Protected:   netsec.WordSet{5},
			AttackValue: 5,
			FreeHeaders: netsec.HeaderSet{4, 5},
			Version:     version,
		})
		if !result.Found {
			t.Fatalf("version %s did not find the aliased write attack", version)
		}
		if result.Prev != 4 || result.Next != 5 {
			t.Fatalf("version %s returned (%d,%d), want (4,5)", version, result.Prev, result.Next)
		}
		if !reflect.DeepEqual(result.AttackAddresses, []uint32{5}) {
			t.Fatalf("version %s attack addresses = %v, want [5]", version, result.AttackAddresses)
		}
		if !reflect.DeepEqual(result.Trace, result.Winner.Trace) {
			t.Fatalf("version %s trace was not copied from the winner", version)
		}
		if len(result.Trace) == 0 {
			t.Fatalf("version %s returned no read/write trace", version)
		}
	}
}

func TestSearchRequiresExactAttackValue(t *testing.T) {
	var mem netsec.Memory
	// The aliased unlink finishes with m[5] = 5, never 4.
	result := netsec.Search(netsec.SearchRequest{
		Memory:      mem,
		Header:      0,
		PrevAllowed: netsec.ValueSet{4},
		NextAllowed: netsec.ValueSet{5},
		Protected:   netsec.WordSet{5},
		AttackValue: 4,
		Version:     netsec.VersionA,
	})
	if result.Found {
		t.Fatalf("accepted m[5]=%d as attack value 4", result.Winner.After[5])
	}
}

func TestSearchLexicographicMinimumSkipsHarmlessPairs(t *testing.T) {
	var mem netsec.Memory
	result := netsec.Search(netsec.SearchRequest{
		Memory: mem,
		Header: 0,
		// Deliberately unsorted and duplicated.
		PrevAllowed: netsec.ValueSet{8, 4, 0, 4},
		NextAllowed: netsec.ValueSet{8, 1, 5, 1},
		Protected:   netsec.WordSet{1},
		AttackValue: 4,
		Version:     netsec.VersionA,
	})
	if !result.Found {
		t.Fatal("expected an attack on m[1]")
	}
	// (0,1), (0,5), and (0,8) are accepted but harmless; (4,1) writes
	// m[1] = 4 without alias. It is the lexicographically first success.
	if result.Prev != 4 || result.Next != 1 {
		t.Fatalf("found (%d,%d), want lexicographically first (4,1)", result.Prev, result.Next)
	}
	if result.Attempts != 4 {
		t.Fatalf("attempts = %d, want 4", result.Attempts)
	}
	if got, want := traceStrings(result.Trace), []string{
		"read m[0] = 4",
		"read m[1] = 1",
		"write m[1] = 4",
		"write m[5] = 1",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trace:\n got %v\nwant %v", got, want)
	}
}

func TestSearchCRejectsForgedNeighbourBAllows(t *testing.T) {
	var mem netsec.Memory
	mem[5], mem[8] = 0, 0
	base := netsec.SearchRequest{
		Memory:      mem,
		Header:      0,
		PrevAllowed: netsec.ValueSet{4},
		NextAllowed: netsec.ValueSet{8},
		Protected:   netsec.WordSet{8},
		AttackValue: 4,
		FreeHeaders: netsec.HeaderSet{4}, // 8 is forged, not registered
	}

	base.Version = netsec.VersionB
	if got := netsec.Search(base); !got.Found || got.Winner.After[8] != 4 {
		t.Fatalf("B did not accept the backlinked forged neighbour: %+v", got)
	}

	base.Version = netsec.VersionC
	if got := netsec.Search(base); got.Found {
		t.Fatalf("C accepted an unregistered neighbour: %+v", got)
	}
}

func TestSearchOutOfBoundsCandidateIsNotSuccess(t *testing.T) {
	max := ^uint32(0)
	var mem netsec.Memory
	result := netsec.Search(netsec.SearchRequest{
		Memory:      mem,
		Header:      0,
		PrevAllowed: netsec.ValueSet{max},
		NextAllowed: netsec.ValueSet{max},
		// The attacker's own setup would put max in m[0]. That is not an
		// unlink write and must not be reported as an exploit.
		Protected:   netsec.WordSet{0},
		AttackValue: max,
		Version:     netsec.VersionA,
	})
	if result.Found {
		t.Fatal("out-of-bounds candidate was reported as a successful attack")
	}
}

func TestSearchDoesNotCountAttackerHeaderSetup(t *testing.T) {
	var mem netsec.Memory
	result := netsec.Search(netsec.SearchRequest{
		Memory:      mem,
		Header:      0,
		PrevAllowed: netsec.ValueSet{4},
		NextAllowed: netsec.ValueSet{8},
		Protected:   netsec.WordSet{0},
		AttackValue: 4,
		Version:     netsec.VersionA,
	})
	if result.Found {
		t.Fatal("attacker-controlled header setup was counted as an unlink attack")
	}
}

func TestSearchInvalidRequest(t *testing.T) {
	var mem netsec.Memory
	result := netsec.Search(netsec.SearchRequest{
		Memory:      mem,
		Header:      2,
		PrevAllowed: netsec.ValueSet{0},
		NextAllowed: netsec.ValueSet{0},
		Protected:   netsec.WordSet{0},
		AttackValue: 0,
		Version:     netsec.VersionA,
	})
	if result.Found || result.Reason != netsec.RejectInvalidHeader {
		t.Fatalf("invalid header result = %+v", result)
	}

	result = netsec.Search(netsec.SearchRequest{
		Memory:      mem,
		Header:      0,
		PrevAllowed: netsec.ValueSet{0},
		NextAllowed: netsec.ValueSet{0},
		Protected:   netsec.WordSet{0},
		AttackValue: 0,
		Version:     netsec.Version(99),
	})
	if result.Found || result.Reason != netsec.RejectInvalidVersion {
		t.Fatalf("invalid version result = %+v", result)
	}
}
