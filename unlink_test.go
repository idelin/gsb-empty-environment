package netsec_test

import (
	"reflect"
	"testing"

	"example.org/netsec/q0019"
)

func traceStrings(trace []netsec.Access) []string {
	out := make([]string, len(trace))
	for i, access := range trace {
		out[i] = access.String()
	}
	return out
}

func TestOverlappingWriteTargetsLastWriteWins(t *testing.T) {
	var mem netsec.Memory
	// header 0: prev=4, next=5. Both writes target m[5]:
	//   next.prev = prev -> m[5] = 4
	//   prev.next = next -> m[5] = 5
	comparison := netsec.ReplayVersions(mem, 0, 4, 5, netsec.HeaderSet{4, 5})

	wantAFinal := uint32(5)
	for _, tc := range []struct {
		name string
		got  netsec.ReplayResult
	}{
		{"A", comparison.A},
		{"B", comparison.B},
		{"C", comparison.C},
	} {
		if !tc.got.Accepted {
			t.Fatalf("version %s rejected valid aliased unlink: %s", tc.name, tc.got.Reason)
		}
		if tc.got.After[5] != wantAFinal {
			t.Fatalf("version %s final m[5] = %d, want %d (second write must win)",
				tc.name, tc.got.After[5], wantAFinal)
		}
		if got, want := tc.got.ChangedWords(), []uint32{5}; !reflect.DeepEqual(got, want) {
			t.Fatalf("version %s changed words = %v, want %v", tc.name, got, want)
		}
	}

	if got, want := traceStrings(comparison.A.Trace), []string{
		"read m[0] = 4",
		"read m[1] = 5",
		"write m[5] = 4",
		"write m[5] = 5",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("A trace:\n got %v\nwant %v", got, want)
	}

	// B and C read the aliased backlink word twice before writing.
	if got, want := traceStrings(comparison.B.Trace), []string{
		"read m[0] = 4",
		"read m[1] = 5",
		"read m[5] = 0",
		"read m[5] = 0",
		"write m[5] = 4",
		"write m[5] = 5",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("B trace:\n got %v\nwant %v", got, want)
	}
}

func TestForgedBackpointerPassesBButNotC(t *testing.T) {
	var mem netsec.Memory
	// Both backlinks point at header 0, so B's chain checks pass. Header 8
	// is deliberately absent from the free registry, so C must reject.
	mem[5] = 0 // prev.next
	mem[8] = 0 // next.prev
	comparison := netsec.ReplayVersions(mem, 0, 4, 8, netsec.HeaderSet{4})

	if !comparison.A.Accepted {
		t.Fatalf("A rejected: %s", comparison.A.Reason)
	}
	if !comparison.B.Accepted {
		t.Fatalf("B rejected forged but backlinked neighbour: %s", comparison.B.Reason)
	}
	if comparison.C.Accepted || comparison.C.Reason != netsec.RejectUnregisteredFree {
		t.Fatalf("C accepted or used wrong reason: accepted=%v reason=%q",
			comparison.C.Accepted, comparison.C.Reason)
	}
	if !reflect.DeepEqual(comparison.C.Before, comparison.C.After) {
		t.Fatalf("C rejection changed memory:\n before %v\n after  %v",
			comparison.C.Before, comparison.C.After)
	}

	if got, want := traceStrings(comparison.B.Trace), []string{
		"read m[0] = 4",
		"read m[1] = 8",
		"read m[8] = 0",
		"read m[5] = 0",
		"write m[8] = 4",
		"write m[5] = 8",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("B trace:\n got %v\nwant %v", got, want)
	}

	if got, want := traceStrings(comparison.C.Trace), []string{
		"read m[0] = 4",
		"read m[1] = 8",
		"read m[8] = 0",
		"read m[5] = 0",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("C trace:\n got %v\nwant %v", got, want)
	}
}

func TestBacklinkMismatchRejectedBeforeAnyWrite(t *testing.T) {
	var mem netsec.Memory
	mem[8] = 1 // next.prev does not point back to header 0
	comparison := netsec.ReplayVersions(mem, 0, 4, 8, netsec.HeaderSet{4, 8})

	if !comparison.A.Accepted {
		t.Fatalf("A should ignore the forged backlink: %s", comparison.A.Reason)
	}
	for _, tc := range []struct {
		name string
		got  netsec.ReplayResult
	}{
		{"B", comparison.B},
		{"C", comparison.C},
	} {
		if tc.got.Accepted || tc.got.Reason != netsec.RejectBacklinkMismatch {
			t.Fatalf("version %s accepted or wrong reason: accepted=%v reason=%q",
				tc.name, tc.got.Accepted, tc.got.Reason)
		}
		if !reflect.DeepEqual(tc.got.Before, tc.got.After) {
			t.Fatalf("version %s rejection modified memory", tc.name)
		}
		if changed := tc.got.ChangedWords(); len(changed) != 0 {
			t.Fatalf("version %s changed words after rejection: %v", tc.name, changed)
		}
	}

	if got, want := traceStrings(comparison.B.Trace), []string{
		"read m[0] = 4",
		"read m[1] = 8",
		"read m[8] = 1",
		"read m[5] = 0",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("B precheck trace:\n got %v\nwant %v", got, want)
	}
}

func TestOutOfBoundsFieldsRejectedAtomically(t *testing.T) {
	max := ^uint32(0)
	for _, tc := range []struct {
		name string
		prev uint32
		next uint32
	}{
		{"both max", max, max},
		{"prev wrap", max, 0},
		{"prev+1 past end", 63, 0},
		{"next past end", 0, 64},
		{"next far past end", 0, max},
	} {
		for _, version := range []netsec.Version{netsec.VersionA, netsec.VersionB, netsec.VersionC} {
			var mem netsec.Memory
			got := netsec.Replay(mem, 0, tc.prev, tc.next, version, netsec.HeaderSet{0, 4, 8})
			if got.Accepted || got.Reason != netsec.RejectOutOfBounds {
				t.Fatalf("%s/%s accepted or wrong reason: accepted=%v reason=%q",
					tc.name, version, got.Accepted, got.Reason)
			}
			if !reflect.DeepEqual(got.Before, got.After) {
				t.Fatalf("%s/%s rejection changed memory", tc.name, version)
			}
			if got.Before[0] != tc.prev || got.Before[1] != tc.next {
				t.Fatalf("%s/%s did not retain the attacker header snapshot", tc.name, version)
			}
			if len(got.Trace) != 2 {
				t.Fatalf("%s/%s trace length = %d, want only the two header reads",
					tc.name, version, len(got.Trace))
			}
			if changed := got.ChangedWords(); len(changed) != 0 {
				t.Fatalf("%s/%s changed words after rejection: %v", tc.name, version, changed)
			}
		}
	}
}

func TestBoundaryFieldAddressesAccepted(t *testing.T) {
	var mem netsec.Memory
	// prev=62 makes prev+1=63; next=63 aliases that word.
	got := netsec.Replay(mem, 0, 62, 63, netsec.VersionA, nil)
	if !got.Accepted {
		t.Fatalf("A rejected in-range boundary fields: %s", got.Reason)
	}
	if got.After[63] != 63 {
		t.Fatalf("final m[63] = %d, want 63", got.After[63])
	}
	if got, want := traceStrings(got.Trace), []string{
		"read m[0] = 62",
		"read m[1] = 63",
		"write m[63] = 62",
		"write m[63] = 63",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("trace:\n got %v\nwant %v", got, want)
	}
}

func TestInvalidHeaderRejected(t *testing.T) {
	for _, header := range []uint32{1, 2, 3, 63, 64, 68} {
		var mem netsec.Memory
		got := netsec.Replay(mem, header, 0, 0, netsec.VersionA, nil)
		if got.Accepted || got.Reason != netsec.RejectInvalidHeader {
			t.Fatalf("header %d accepted or wrong reason: accepted=%v reason=%q",
				header, got.Accepted, got.Reason)
		}
		if len(got.Trace) != 0 || !reflect.DeepEqual(got.Before, mem) || !reflect.DeepEqual(got.After, mem) {
			t.Fatalf("header %d invalid request performed work", header)
		}
	}
}

func TestSentinelUnlinkIsPlainChainBreak(t *testing.T) {
	free := netsec.HeaderSet{0, 4, 8, 12, 16, 20, 24, 28, 32, 36, 40, 44, 48, 52, 56, 60}

	var head netsec.Memory
	head[4], head[5] = 0, 8 // current header
	head[1], head[8] = 4, 4 // prev.next and next.prev

	var tail netsec.Memory
	tail[56], tail[57] = 52, 60
	tail[53], tail[60] = 56, 56

	for _, tc := range []struct {
		name   string
		mem    netsec.Memory
		header uint32
		prev   uint32
		next   uint32
	}{
		{"head sentinel", head, 4, 0, 8},
		{"tail sentinel", tail, 56, 52, 60},
	} {
		comparison := netsec.ReplayVersions(tc.mem, tc.header, tc.prev, tc.next, free)
		for _, version := range []struct {
			name string
			got  netsec.ReplayResult
		}{
			{"A", comparison.A},
			{"B", comparison.B},
			{"C", comparison.C},
		} {
			if !version.got.Accepted {
				t.Fatalf("%s/%s rejected normal sentinel unlink: %s",
					tc.name, version.name, version.got.Reason)
			}
		}

		result := netsec.Search(netsec.SearchRequest{
			Memory:      tc.mem,
			Header:      tc.header,
			PrevAllowed: netsec.ValueSet{tc.prev},
			NextAllowed: netsec.ValueSet{tc.next},
			Protected:   netsec.WordSet{tc.prev + 1, tc.next},
			AttackValue: 0xdeadbeef,
			FreeHeaders: free,
			Version:     netsec.VersionC,
		})
		if result.Found {
			t.Fatalf("%s: normal chain break was reported as attack at %v",
				tc.name, result.AttackAddresses)
		}
	}
}
