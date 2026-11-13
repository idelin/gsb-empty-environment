package netsec

import (
	"bytes"
	"testing"
)

func TestOutOfOrderReassembly(t *testing.T) {
	t.Parallel()

	for _, policy := range []Policy{FirstArrival, LastArrival} {
		policy := policy
		t.Run(policy.String(), func(t *testing.T) {
			t.Parallel()

			r := NewReassembler(policy)
			for _, segment := range []Segment{
				{Seq: 6, Data: []byte("ghi")},
				{Seq: 0, Data: []byte("abc")},
				{Seq: 3, Data: []byte("def")},
			} {
				if conflicts := r.Feed(segment); len(conflicts) != 0 {
					t.Fatalf("unexpected conflict for in-order data: %+v", conflicts)
				}
			}
			if got, want := string(r.Stream()), "abcdefghi"; got != want {
				t.Fatalf("stream = %q, want %q", got, want)
			}
		})
	}
}

func TestConsistentOverlapDoesNotAlert(t *testing.T) {
	t.Parallel()

	for _, policy := range []Policy{FirstArrival, LastArrival} {
		policy := policy
		t.Run(policy.String(), func(t *testing.T) {
			t.Parallel()

			r := NewReassembler(policy)
			if conflicts := r.Add(0, []byte("abcdefgh")); len(conflicts) != 0 {
				t.Fatalf("First segment produced conflict: %+v", conflicts)
			}
			if conflicts := r.Add(3, []byte("defgh")); len(conflicts) != 0 {
				t.Fatalf("Identical overlap produced conflict: %+v", conflicts)
			}
			if conflicts := r.Add(2, []byte("cde")); len(conflicts) != 0 {
				t.Fatalf("Identical partial overlap produced conflict: %+v", conflicts)
			}
			if got, want := string(r.Stream()), "abcdefgh"; got != want {
				t.Fatalf("stream = %q, want %q", got, want)
			}
			if got := len(r.Conflicts()); got != 0 {
				t.Fatalf("recorded %d conflicts, want 0", got)
			}
		})
	}
}

func TestInconsistentOverlapBeforeAndAfter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy Policy
		want   string
	}{
		{name: "first-arrival", policy: FirstArrival, want: "AB0123456789YZ"},
		{name: "last-arrival", policy: LastArrival, want: "ABCD234567WXYZ"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := NewReassembler(tt.policy)
			r.Add(100, []byte("0123456789"))

			prefixConflicts := r.Add(98, []byte("ABCD"))
			if len(prefixConflicts) != 1 {
				t.Fatalf("prefix overlap returned %d conflicts, want 1", len(prefixConflicts))
			}
			assertConflict(t, prefixConflicts[0], 100, 102, 2)

			suffixConflicts := r.Add(108, []byte("WXYZ"))
			if len(suffixConflicts) != 1 {
				t.Fatalf("suffix overlap returned %d conflicts, want 1", len(suffixConflicts))
			}
			assertConflict(t, suffixConflicts[0], 108, 110, 2)

			if got := string(r.Stream()); got != tt.want {
				t.Fatalf("stream = %q, want %q", got, tt.want)
			}
			if got, want := len(r.Conflicts()), 2; got != want {
				t.Fatalf("recorded %d conflicts, want %d", got, want)
			}
		})
	}
}

func TestConflictRunsAreReportedSeparately(t *testing.T) {
	t.Parallel()

	r := NewReassembler(FirstArrival)
	r.Add(0, []byte("abcdef"))
	conflicts := r.Add(2, []byte("XYeZ"))
	if len(conflicts) != 2 {
		t.Fatalf("returned %d conflicts, want 2: %+v", len(conflicts), conflicts)
	}
	assertConflict(t, conflicts[0], 2, 4, 2)
	assertConflict(t, conflicts[1], 5, 6, 1)
	if got, want := string(r.Stream()), "abcdef"; got != want {
		t.Fatalf("stream = %q, want %q", got, want)
	}
}

func TestContainedOverlapLastArrivalWins(t *testing.T) {
	t.Parallel()

	r := NewReassembler(LastArrival)
	r.Add(0, []byte("abcdef"))
	conflicts := r.Add(2, []byte("XY"))
	if len(conflicts) != 1 {
		t.Fatalf("returned %d conflicts, want 1", len(conflicts))
	}
	assertConflict(t, conflicts[0], 2, 4, 2)
	if got, want := string(r.Stream()), "abXYef"; got != want {
		t.Fatalf("stream = %q, want %q", got, want)
	}
}

func TestSequenceWraparoundAssembly(t *testing.T) {
	t.Parallel()

	first := []byte("ABCDEFGH")
	second := []byte("IJKLMNOP")
	for _, policy := range []Policy{FirstArrival, LastArrival} {
		policy := policy
		t.Run(policy.String(), func(t *testing.T) {
			t.Parallel()

			// The later half arrives first, then the segment immediately
			// before the 32-bit wrap point.
			r := NewReassembler(policy)
			if conflicts := r.Add(0, second); len(conflicts) != 0 {
				t.Fatalf("later segment produced conflict: %+v", conflicts)
			}
			if conflicts := r.Add(0xfffffff8, first); len(conflicts) != 0 {
				t.Fatalf("wrapped segment produced conflict: %+v", conflicts)
			}
			if got, want := string(r.Stream()), "ABCDEFGHIJKLMNOP"; got != want {
				t.Fatalf("stream = %q, want %q", got, want)
			}
		})
	}
}

func TestConflictIntervalWraps(t *testing.T) {
	t.Parallel()

	r := NewReassembler(LastArrival)
	r.Add(0xfffffffc, []byte("ABCD"))
	conflicts := r.Add(0xfffffffe, []byte("WXYZ"))
	if len(conflicts) != 1 {
		t.Fatalf("returned %d conflicts, want 1", len(conflicts))
	}
	conflict := conflicts[0]
	if conflict.Start != 0xfffffffe || conflict.End != 0 {
		t.Fatalf("conflict sequence interval = [%#x, %#x), want [0xfffffffe, 0)",
			conflict.Start, conflict.End)
	}
	if conflict.StartOffset != 2 || conflict.EndOffset != 4 || conflict.Length != 2 {
		t.Fatalf("conflict offsets = [%d, %d) length %d, want [2, 4) length 2",
			conflict.StartOffset, conflict.EndOffset, conflict.Length)
	}
	if got, want := string(r.Stream()), "ABWXYZ"; got != want {
		t.Fatalf("stream = %q, want %q", got, want)
	}
}

func TestConflictHandlerAndReassembleHelper(t *testing.T) {
	t.Parallel()

	r := NewReassembler(FirstArrival)
	var handled []Conflict
	r.SetConflictHandler(func(conflict Conflict) {
		handled = append(handled, conflict)
	})
	r.Add(0, []byte("abcd"))
	r.Add(1, []byte("XX"))
	if len(handled) != 1 {
		t.Fatalf("handler saw %d conflicts, want 1", len(handled))
	}

	stream, conflicts := Reassemble(LastArrival, []Segment{
		{Seq: 0, Data: []byte("abcd")},
		{Seq: 1, Data: []byte("XX")},
	})
	if got, want := string(stream), "aXXd"; got != want {
		t.Fatalf("Reassemble stream = %q, want %q", got, want)
	}
	if len(conflicts) != 1 {
		t.Fatalf("Reassemble returned %d conflicts, want 1", len(conflicts))
	}
	assertConflict(t, conflicts[0], 1, 3, 2)
}

func assertConflict(t *testing.T, conflict Conflict, start, end uint32, length int) {
	t.Helper()
	if conflict.Start != start || conflict.End != end || conflict.Length != length {
		t.Fatalf("conflict = [%#x, %#x) length %d, want [%#x, %#x) length %d",
			conflict.Start, conflict.End, conflict.Length, start, end, length)
	}
}

func TestIdenticalOverlapLeavesInputUnmodified(t *testing.T) {
	t.Parallel()

	payload := []byte("abcdef")
	r := NewReassembler(FirstArrival)
	r.Add(0, payload)
	payload[0] = 'z'
	if got, want := string(r.Stream()), "abcdef"; got != want {
		t.Fatalf("stream = %q, want %q; reassembler retained caller storage", got, want)
	}
}

func TestNoData(t *testing.T) {
	t.Parallel()

	r := NewReassembler(FirstArrival)
	if conflicts := r.Add(7, nil); len(conflicts) != 0 {
		t.Fatalf("empty segment produced conflicts: %+v", conflicts)
	}
	if got := r.Stream(); got != nil {
		t.Fatalf("empty reassembler returned %q, want nil", got)
	}
	if !bytes.Equal(r.Data(), nil) {
		t.Fatalf("Data should match Stream")
	}
}
