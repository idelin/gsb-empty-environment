package netsec

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type referenceState struct {
	policy Policy
	base   uint32
	have   bool
	bytes  map[int64]byte
}

func (s *referenceState) add(seq uint32, data []byte) []Conflict {
	if len(data) == 0 {
		return nil
	}
	if !s.have {
		s.base = seq
		s.have = true
	}
	off := int64(int32(seq - s.base))
	var positions []int64
	for i, b := range data {
		pos := off + int64(i)
		old, exists := s.bytes[pos]
		if exists && old != b {
			positions = append(positions, pos)
		}
		if s.policy == LastArrival || !exists {
			s.bytes[pos] = b
		}
	}
	return spansToConflicts(s.base, positions)
}

func spansToConflicts(base uint32, positions []int64) []Conflict {
	if len(positions) == 0 {
		return nil
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i] < positions[j] })
	var result []Conflict
	start, end := positions[0], positions[0]+1
	for _, pos := range positions[1:] {
		if pos == end {
			end++
			continue
		}
		result = append(result, Conflict{
			Start:       uint32(int64(base) + start),
			End:         uint32(int64(base) + end),
			StartOffset: start,
			EndOffset:   end,
			Length:      int(end - start),
		})
		start, end = pos, pos+1
	}
	result = append(result, Conflict{
		Start:       uint32(int64(base) + start),
		End:         uint32(int64(base) + end),
		StartOffset: start,
		EndOffset:   end,
		Length:      int(end - start),
	})
	return result
}

func (s *referenceState) stream() []byte {
	if !s.have || len(s.bytes) == 0 {
		return nil
	}
	min := int64(1<<63 - 1)
	for pos := range s.bytes {
		if pos < min {
			min = pos
		}
	}
	var result []byte
	for pos := min; ; pos++ {
		b, ok := s.bytes[pos]
		if !ok {
			break
		}
		result = append(result, b)
	}
	return result
}

func TestReferenceProperty(t *testing.T) {
	for _, policy := range []Policy{FirstArrival, LastArrival} {
		rng := rand.New(rand.NewSource(719))
		r := NewReassembler(policy)
		ref := referenceState{policy: policy, bytes: map[int64]byte{}}
		base := uint32(0xffffffe0)
		var history []Segment
		for step := 0; step < 2000; step++ {
			seq := base + uint32(int32(rng.Intn(81)-40))
			length := rng.Intn(9)
			payload := make([]byte, length)
			for i := range payload {
				payload[i] = byte(rng.Intn(4))
			}
			gotConflicts := r.Add(seq, payload)
			history = append(history, Segment{Seq: seq, Data: payload})
			wantConflicts := ref.add(seq, payload)
			if !reflect.DeepEqual(gotConflicts, wantConflicts) {
				t.Logf("history: %+v", history)
				t.Fatalf("%s step %d seq %#x payload %q: conflicts %+v, want %+v",
					policy, step, seq, payload, gotConflicts, wantConflicts)
			}
			if got, want := string(r.Stream()), string(ref.stream()); got != want {
				t.Fatalf("%s step %d: stream %q, want %q", policy, step, got, want)
			}
		}
	}
}
