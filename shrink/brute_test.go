package shrink_test

import (
	"math"
	"testing"

	"example.org/netsec/q0497/shrink"
)

// bruteCandidates is an independent recursive reference implementation that
// materializes the full domain and sorts it by the contract ordering.
func bruteCandidates(t *testing.T, s *shrink.Schema) []bruteCandidate {
	t.Helper()
	n := s.FieldCount()
	states := []bruteState{{present: make([]bool, n), values: make([]shrink.Value, n)}}

	for i := 0; i < n; i++ {
		field, ok := s.Field(i)
		if !ok {
			t.Fatalf("missing field %d", i)
		}
		var domain []shrink.Value
		switch s.Kind(i) {
		case shrink.IntegerKind:
			for v := field.Integer.Min; ; v++ {
				domain = append(domain, shrink.Value{Int: v})
				if v == field.Integer.Max {
					break
				}
			}
		case shrink.StringKind:
			for length := 0; length <= field.String.MaxLen; length++ {
				buf := make([]byte, length)
				for j := range buf {
					buf[j] = 'a'
				}
				domain = append(domain, shrink.Value{Str: string(buf)})
			}
		}

		var next []bruteState
		for _, st := range states {
			if field.Optional {
				absent := bruteState{
					present: append([]bool(nil), st.present...),
					values:  append([]shrink.Value(nil), st.values...),
				}
				next = append(next, absent)
			}
			for _, v := range domain {
				updated := bruteState{
					present: append([]bool(nil), st.present...),
					values:  append([]shrink.Value(nil), st.values...),
				}
				updated.present[i] = true
				updated.values[i] = v
				next = append(next, updated)
			}
		}
		states = next
	}

	out := make([]bruteCandidate, 0, len(states))
	for _, st := range states {
		out = append(out, bruteCandidate{
			present: st.present,
			values:  st.values,
			bytes:   bruteBytes(s, st),
		})
	}
	sortBrute(out, s)
	return out
}

type bruteCandidate struct {
	present []bool
	values  []shrink.Value
	bytes   int
}

type bruteState struct {
	present []bool
	values  []shrink.Value
}

func bruteBytes(s *shrink.Schema, st bruteState) int {
	optional := s.OptionalCount()
	mask := 0
	if optional > 0 {
		mask = (optional + 7) / 8
	}
	total := mask
	for i, ok := range st.present {
		if !ok {
			continue
		}
		switch s.Kind(i) {
		case shrink.IntegerKind:
			total += refVarintLen(st.values[i].Int)
		case shrink.StringKind:
			total += len(st.values[i].Str)
		}
	}
	return total
}

func refVarintLen(v int64) int {
	ux := uint64(v) << 1
	if v < 0 {
		ux = ^ux
	}
	n := 1
	for ux >= 0x80 {
		ux >>= 7
		n++
	}
	return n
}

func sortBrute(candidates []bruteCandidate, s *shrink.Schema) {
	domains := referenceDomains(s)
	less := func(a, b bruteCandidate) bool {
		if ao, bo := optionalCount(a, s), optionalCount(b, s); ao != bo {
			return ao < bo
		}
		if a.bytes != b.bytes {
			return a.bytes < b.bytes
		}
		if ma, mb := presenceMask(a, s), presenceMask(b, s); ma != mb {
			return ma < mb
		}
		for i := range a.values {
			if !a.present[i] {
				continue
			}
			ia, ib := indexOf(domains[i], a.values[i]), indexOf(domains[i], b.values[i])
			if ia != ib {
				return ia < ib
			}
		}
		return false
	}
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && less(candidates[j], candidates[j-1]); j-- {
			candidates[j-1], candidates[j] = candidates[j], candidates[j-1]
		}
	}
}

func optionalCount(c bruteCandidate, s *shrink.Schema) int {
	count := 0
	for i := range c.present {
		field, _ := s.Field(i)
		if field.Optional && c.present[i] {
			count++
		}
	}
	return count
}

func presenceMask(c bruteCandidate, s *shrink.Schema) int {
	mask, bit := 0, 0
	for i := range c.present {
		field, _ := s.Field(i)
		if !field.Optional {
			continue
		}
		if c.present[i] {
			mask |= 1 << bit
		}
		bit++
	}
	return mask
}

// referenceDomains mirrors the package's cost-sorted per-field domains.
func referenceDomains(s *shrink.Schema) [][]shrink.Value {
	domains := make([][]shrink.Value, s.FieldCount())
	for i := range domains {
		field, _ := s.Field(i)
		switch s.Kind(i) {
		case shrink.IntegerKind:
			for v := field.Integer.Min; ; v++ {
				domains[i] = append(domains[i], shrink.Value{Int: v})
				if v == field.Integer.Max {
					break
				}
			}
			sortIntValues(domains[i])
		case shrink.StringKind:
			for length := 0; length <= field.String.MaxLen; length++ {
				buf := make([]byte, length)
				for j := range buf {
					buf[j] = 'a'
				}
				domains[i] = append(domains[i], shrink.Value{Str: string(buf)})
			}
		}
	}
	return domains
}

func sortIntValues(values []shrink.Value) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0; j-- {
			a, b := values[j], values[j-1]
			ca, cb := refVarintLen(a.Int), refVarintLen(b.Int)
			if ca < cb || (ca == cb && a.Int < b.Int) {
				values[j-1], values[j] = a, b
			}
		}
	}
}

func indexOf(domain []shrink.Value, v shrink.Value) int {
	for i := range domain {
		if domain[i] == v {
			return i
		}
	}
	return -1
}

func TestEnumeratorMatchesBruteForce(t *testing.T) {
	schemas := []*shrink.Schema{
		mustSchema(t, "a", "A-1", []shrink.Field{
			intField("code", -2, 2),
			optIntField("flag", 3, 5),
			optStrField("label", 2),
		}),
		mustSchema(t, "b", "B-1", []shrink.Field{
			strField("name", 2),
			optStrField("note", 2),
		}),
		mustSchema(t, "c", "C-1", []shrink.Field{
			intField("a", 100, 102),
			intField("b", -1, 1),
			optIntField("c", 0, 1),
		}),
	}

	for _, s := range schemas {
		t.Run(s.Name(), func(t *testing.T) {
			want := bruteCandidates(t, s)

			// A precondition that keeps roughly half the domain but never
			// changes ordering.
			pre := func(c *shrink.Candidate) bool {
				v, ok := c.ValueAt(0)
				if !ok {
					return false
				}
				if s.Kind(0) == shrink.IntegerKind {
					return v.Int%2 == 0
				}
				return len(v.Str)%2 == 0
			}

			enum, err := shrink.NewEnumerator(s, pre)
			if err != nil {
				t.Fatalf("NewEnumerator: %v", err)
			}

			var got []bruteCandidate
			for {
				cand, ok := enum.Next()
				if !ok {
					break
				}
				present, values := cand.Snapshot()
				if cand.TotalBytes() != bruteBytes(s, bruteState{present, values}) {
					t.Fatalf("TotalBytes mismatch for %v", present)
				}
				got = append(got, bruteCandidate{present: present, values: values, bytes: cand.TotalBytes()})
			}

			filtered := want[:0:0]
			for _, c := range want {
				v := c.values[0]
				var keep bool
				if s.Kind(0) == shrink.IntegerKind {
					keep = v.Int%2 == 0
				} else {
					keep = len(v.Str)%2 == 0
				}
				if keep {
					filtered = append(filtered, c)
				}
			}

			if len(got) != len(filtered) {
				t.Fatalf("count = %d, want %d", len(got), len(filtered))
			}
			for i := range filtered {
				if !sameCandidate(got[i], filtered[i]) {
					t.Fatalf("candidate %d mismatch:\n got  %+v\n want %+v", i, got[i], filtered[i])
				}
			}
		})
	}
}

func sameCandidate(a, b bruteCandidate) bool {
	for i := range a.present {
		if a.present[i] != b.present[i] {
			return false
		}
	}
	for i := range a.values {
		if a.values[i] != b.values[i] {
			return false
		}
	}
	return a.bytes == b.bytes
}

func TestShrinkBruteForceGlobalMinimum(t *testing.T) {
	s := mustSchema(t, "packet", "ASSERT-BRUTE", []shrink.Field{
		intField("code", -3, 3),
		optStrField("label", 3),
		optIntField("flag", 0, 3),
	})

	pre := func(c *shrink.Candidate) bool {
		if v, ok := c.ValueAt(1); ok && len(v.Str) == 1 {
			return false
		}
		return true
	}
	fail := func(c *shrink.Candidate) bool {
		if v, ok := c.ValueAt(2); ok && v.Int >= 3 {
			return true
		}
		if v, ok := c.ValueAt(1); ok && len(v.Str) == 3 {
			return true
		}
		return false
	}

	result, err := shrink.Shrink(shrink.Request{
		Schema:       s,
		Precondition: pre,
		Reproduce:    fail,
		Budget:       s.Size(),
	})
	if err != nil {
		t.Fatalf("Shrink: %v", err)
	}

	all := bruteCandidates(t, s)
	var want *bruteCandidate
	for i := range all {
		c := all[i]
		// Mirror the precondition.
		if c.present[1] && len(c.values[1].Str) == 1 {
			continue
		}
		failing := (c.present[2] && c.values[2].Int >= 3) ||
			(c.present[1] && len(c.values[1].Str) == 3)
		if failing {
			want = &all[i]
			break
		}
	}
	if want == nil {
		t.Fatal("brute force found no failing candidate")
	}

	present, values := result.Best.Snapshot()
	got := bruteCandidate{present: present, values: values, bytes: result.Best.TotalBytes()}
	if !sameCandidate(got, *want) {
		t.Fatalf("global minimum mismatch:\n got  %+v\n want %+v", got, *want)
	}
}

func TestSizeSaturates(t *testing.T) {
	s := mustSchema(t, "packet", "ASSERT-SIZE", []shrink.Field{
		intField("a", 0, int64(shrink.MaxIntRange)),
		intField("b", 0, int64(shrink.MaxIntRange)),
	})
	if s.Size() != (shrink.MaxIntRange+1)*(shrink.MaxIntRange+1) {
		t.Fatalf("Size = %d, want exact product", s.Size())
	}

	// Three large factors overflow any int product and must saturate.
	saturated := mustSchema(t, "big", "ASSERT-SIZE-2", []shrink.Field{
		intField("a", 0, int64(shrink.MaxIntRange)),
		intField("b", 0, int64(shrink.MaxIntRange)),
		intField("c", 0, int64(shrink.MaxIntRange)),
		optIntField("d", 0, int64(shrink.MaxIntRange)-1),
	})
	if saturated.Size() != math.MaxInt {
		t.Fatalf("Size = %d, want MaxInt", saturated.Size())
	}
}
