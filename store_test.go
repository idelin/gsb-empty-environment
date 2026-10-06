package reservation

import (
	"fmt"
	"reflect"
	"testing"
)

// seed loads the initial committed state.
func seed(s *Store, kvs map[int]string) {
	tx := s.Begin()
	for k, v := range kvs {
		tx.Set(k, v)
	}
	if err := tx.Commit(); err != nil {
		panic(err)
	}
}

func TestInsertInsidePredicateAborts(t *testing.T) {
	s := NewStore()
	seed(s, map[int]string{1: "a", 5: "b"})

	reader := s.Begin()
	reader.Scan(0, 10)

	writer := s.Begin()
	writer.Set(7, "new") // insert inside [0,10)
	if err := writer.Commit(); err != nil {
		t.Fatalf("writer commit: %v", err)
	}
	if err := reader.Commit(); err != ErrConflict {
		t.Fatalf("reader commit = %v, want ErrConflict", err)
	}
}

func TestDeleteInsidePredicateAborts(t *testing.T) {
	s := NewStore()
	seed(s, map[int]string{1: "a", 5: "b"})

	reader := s.Begin()
	reader.Scan(0, 10)

	writer := s.Begin()
	writer.Delete(5) // delete inside [0,10)
	if err := writer.Commit(); err != nil {
		t.Fatalf("writer commit: %v", err)
	}
	if err := reader.Commit(); err != ErrConflict {
		t.Fatalf("reader commit = %v, want ErrConflict", err)
	}
}

func TestWriteOutsidePredicateSucceeds(t *testing.T) {
	s := NewStore()
	seed(s, map[int]string{1: "a"})

	reader := s.Begin()
	reader.Scan(0, 10)

	writer := s.Begin()
	writer.Set(10, "x") // Hi bound is exclusive
	writer.Set(-3, "y") // below Lo
	if err := writer.Commit(); err != nil {
		t.Fatalf("writer commit: %v", err)
	}
	if err := reader.Commit(); err != nil {
		t.Fatalf("reader commit = %v, want nil", err)
	}
}

func TestEmptyScanProtected(t *testing.T) {
	s := NewStore()

	reader := s.Begin()
	if got := reader.Scan(100, 110); len(got) != 0 {
		t.Fatalf("scan = %v, want empty", got)
	}

	writer := s.Begin()
	writer.Set(105, "phantom")
	if err := writer.Commit(); err != nil {
		t.Fatalf("writer commit: %v", err)
	}
	if err := reader.Commit(); err != ErrConflict {
		t.Fatalf("reader commit = %v, want ErrConflict", err)
	}
}

func TestBoundaryAdjacency(t *testing.T) {
	cases := []struct {
		name     string
		pred     Interval
		writeKey int
		wantErr  bool
	}{
		{"left interval, key at Hi-1", Interval{0, 10}, 9, true},
		{"left interval, key at Hi (adjacent)", Interval{0, 10}, 10, false},
		{"right interval, key at Lo", Interval{10, 20}, 10, true},
		{"right interval, key at Lo-1 (adjacent)", Interval{10, 20}, 9, false},
		{"point read hit", Interval{5, 6}, 5, true},
		{"point read miss", Interval{5, 6}, 6, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewStore()
			reader := s.Begin()
			reader.Scan(c.pred.Lo, c.pred.Hi)
			writer := s.Begin()
			writer.Set(c.writeKey, "v")
			if err := writer.Commit(); err != nil {
				t.Fatalf("writer commit: %v", err)
			}
			err := reader.Commit()
			if c.wantErr && err != ErrConflict {
				t.Fatalf("commit = %v, want ErrConflict", err)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("commit = %v, want nil", err)
			}
		})
	}
}

// --- Serial enumeration over up to four transactions ---

// txnSpec is a transaction template: scan predicates plus a write set.
type txnSpec struct {
	name  string
	scans []Interval
	sets  map[int]string
	dels  []int
}

// specPool mixes boundary-adjacent intervals [0,10), [10,20), [20,30),
// an empty region, point reads, puts and deletes.
var specPool = []txnSpec{
	{"A: scan[0,10) put 5", []Interval{{0, 10}}, map[int]string{5: "A"}, nil},
	{"B: scan[0,10) put 10 (adjacent Hi)", []Interval{{0, 10}}, map[int]string{10: "B"}, nil},
	{"C: scan[10,20) put 10 (boundary Lo)", []Interval{{10, 20}}, map[int]string{10: "C"}, nil},
	{"D: scan[10,20) put 9 (adjacent Lo-1)", []Interval{{10, 20}}, map[int]string{9: "D"}, nil},
	{"E: scan empty [100,110) put 105", []Interval{{100, 110}}, map[int]string{105: "E"}, nil},
	{"F: scan[0,30) delete 15", []Interval{{0, 30}}, nil, []int{15}},
	{"G: get 5 put 20", []Interval{{5, 6}}, map[int]string{20: "G"}, nil},
	{"H: scan[10,20) scan[20,30) put 25", []Interval{{10, 20}, {20, 30}}, map[int]string{25: "H"}, nil},
}

// model is an independent re-implementation used as the oracle.
type model struct {
	data    map[int]string
	seq     uint64
	history []WriteRecord
}

func (m *model) run(spec txnSpec) (committed bool, scans [][]Entry) {
	snapshot := m.seq
	for _, iv := range spec.scans {
		var got []Entry
		for k, v := range m.data {
			if iv.Lo <= k && k < iv.Hi {
				got = append(got, Entry{k, v})
			}
		}
		sortEntries(got)
		scans = append(scans, got)
	}
	for _, rec := range m.history {
		if rec.Seq <= snapshot {
			continue
		}
		for _, iv := range spec.scans {
			if iv.Contains(rec.Key) {
				return false, scans
			}
		}
	}
	m.seq++
	for k, v := range spec.sets {
		m.data[k] = v
		m.history = append(m.history, WriteRecord{Seq: m.seq, Key: k, Kind: put})
	}
	for _, k := range spec.dels {
		delete(m.data, k)
		m.history = append(m.history, WriteRecord{Seq: m.seq, Key: k, Kind: del})
	}
	return true, scans
}

func sortEntries(e []Entry) {
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && e[j-1].Key > e[j].Key; j-- {
			e[j-1], e[j] = e[j], e[j-1]
		}
	}
}

// runOnStore executes one spec against the real store.
func runOnStore(s *Store, spec txnSpec) (committed bool, scans [][]Entry) {
	tx := s.Begin()
	for _, iv := range spec.scans {
		scans = append(scans, tx.Scan(iv.Lo, iv.Hi))
	}
	for k, v := range spec.sets {
		tx.Set(k, v)
	}
	for _, k := range spec.dels {
		tx.Delete(k)
	}
	return tx.Commit() == nil, scans
}

// TestSerialEnumerationUpToFour runs every sequence of 1..4 specs drawn
// from specPool, serially, and checks commit decisions, scan results,
// final state, and committed history against the independent model.
func TestSerialEnumerationUpToFour(t *testing.T) {
	initial := map[int]string{5: "v5", 10: "v10", 15: "v15", 25: "v25"}
	n := len(specPool)
	total := 0
	for length := 1; length <= 4; length++ {
		indices := make([]int, length)
		count := 1
		for i := 0; i < length; i++ {
			count *= n
		}
		for c := 0; c < count; c++ {
			x := c
			for i := 0; i < length; i++ {
				indices[i] = x % n
				x /= n
			}
			checkSequence(t, initial, indices)
			total++
		}
	}
	t.Logf("checked %d serial sequences", total)
}

func checkSequence(t *testing.T, initial map[int]string, indices []int) {
	s := NewStore()
	seed(s, initial)
	m := &model{data: map[int]string{}}
	for k, v := range initial {
		m.data[k] = v
	}
	m.seq = 1 // seed committed as one transaction at seq 1

	label := ""
	for _, i := range indices {
		label += specPool[i].name + " | "
	}

	for _, i := range indices {
		spec := specPool[i]
		gotCommitted, gotScans := runOnStore(s, spec)
		wantCommitted, wantScans := m.run(spec)
		if gotCommitted != wantCommitted {
			t.Fatalf("[%s] spec %q committed=%v, want %v", label, spec.name, gotCommitted, wantCommitted)
		}
		if !reflect.DeepEqual(gotScans, wantScans) {
			t.Fatalf("[%s] spec %q scans=%v, want %v", label, spec.name, gotScans, wantScans)
		}
	}

	// Final committed state must match the model.
	for k, v := range m.data {
		tx := s.Begin()
		got, ok := tx.Get(k)
		if !ok || got != v {
			t.Fatalf("[%s] final key %d = %q,%v; want %q,true", label, k, got, ok, v)
		}
	}
	// Committed history past the seed (seq > 1) must match the model exactly.
	var gotHist []WriteRecord
	for _, rec := range s.History() {
		if rec.Seq > 1 {
			gotHist = append(gotHist, rec)
		}
	}
	if len(gotHist) != len(m.history) {
		t.Fatalf("[%s] history len = %d, want %d\n got: %v\nwant: %v",
			label, len(gotHist), len(m.history), gotHist, m.history)
	}
	for i := range gotHist {
		if gotHist[i] != m.history[i] {
			t.Fatalf("[%s] history[%d] = %+v, want %+v", label, i, gotHist[i], m.history[i])
		}
	}
}

func Example() {
	s := NewStore()
	seed(s, map[int]string{1: "a", 2: "b"})

	booking := s.Begin()
	fmt.Println("seen:", booking.Scan(0, 10))

	other := s.Begin()
	other.Set(3, "c") // insert inside [0,10)
	_ = other.Commit()

	fmt.Println("booking commit:", booking.Commit())
	// Output:
	// seen: [{1 a} {2 b}]
	// booking commit: reservation: serialization conflict
}
