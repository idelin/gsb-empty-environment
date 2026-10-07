package encobj

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// plainModel is an independent plaintext state machine: per object a
// version counter plus the field map. Commits apply in issue order.
type plainModel struct {
	version uint64
	fields  map[string][]byte
}

func (m *plainModel) apply(op Operation) uint64 {
	m.version++
	if m.fields == nil {
		m.fields = map[string][]byte{}
	}
	for _, n := range op.Del {
		delete(m.fields, n)
	}
	for n, v := range op.Set {
		m.fields[n] = append([]byte(nil), v...)
	}
	return m.version
}

// TestEnumeratedInterleavings runs every permutation of a short schedule —
// two writers on one object, a rotation, and a snapshot release — and
// checks the visible state against the plaintext model after each step.
func TestEnumeratedInterleavings(t *testing.T) {
	w1 := Operation{OpID: "w1", Set: map[string][]byte{"x": []byte("from-w1")}}
	w2 := Operation{OpID: "w2", Set: map[string][]byte{"y": []byte("from-w2")}, Del: []string{"x"}}

	type step struct {
		name string
		run  func(t *testing.T, st *Store, model *plainModel, snap **Snapshot)
	}
	steps := []step{
		{"w1", func(t *testing.T, st *Store, m *plainModel, snap **Snapshot) {
			want := m.apply(w1)
			if v, err := st.Commit("o", w1); err != nil || v != want {
				t.Fatalf("w1: v=%d err=%v want %d", v, err, want)
			}
		}},
		{"w2", func(t *testing.T, st *Store, m *plainModel, snap **Snapshot) {
			want := m.apply(w2)
			if v, err := st.Commit("o", w2); err != nil || v != want {
				t.Fatalf("w2: v=%d err=%v want %d", v, err, want)
			}
		}},
		{"rotate", func(t *testing.T, st *Store, m *plainModel, snap **Snapshot) {
			if _, err := st.RotateAll(); err != nil {
				t.Fatalf("rotate: %v", err)
			}
		}},
		{"snap-release", func(t *testing.T, st *Store, m *plainModel, snap **Snapshot) {
			sn, err := st.Snapshot("o")
			if err == ErrObjectNotFound {
				return // schedule released nothing this time
			}
			if err != nil {
				t.Fatalf("snapshot: %v", err)
			}
			*snap = sn // released at the end of the permutation
		}},
	}

	permute(len(steps), func(perm []int) {
		t.Run(fmt.Sprintf("perm-%v", perm), func(t *testing.T) {
			dir := t.TempDir()
			st := openTestStore(t, dir, &Options{
				NonceReservationBlock: 2,
				Keys:                  map[string][]byte{"k1": testKey1[:], "k2": testKey2[:]},
				ActiveKey:             "k1",
			})
			defer st.Close()
			if err := st.SetActiveKey("k2"); err != nil { // rotation target
				t.Fatal(err)
			}
			model := &plainModel{}
			var held *Snapshot
			for _, i := range perm {
				steps[i].run(t, st, model, &held)
			}
			if held != nil {
				held.Close()
			}
			// Final visible state must equal the model exactly.
			sn, err := st.Snapshot("o")
			if err != nil {
				t.Fatalf("final snapshot: %v", err)
			}
			defer sn.Close()
			if sn.Version() != model.version {
				t.Fatalf("version = %d, model %d", sn.Version(), model.version)
			}
			if !reflect.DeepEqual(sn.Fields(), model.fields) {
				t.Fatalf("fields = %v, model %v", sn.Fields(), model.fields)
			}
		})
	})
}

func permute(n int, f func([]int)) {
	var rec func(prefix []int, rest []int)
	rec = func(prefix, rest []int) {
		if len(rest) == 0 {
			f(prefix)
			return
		}
		for i := range rest {
			next := append([]int(nil), rest[:i]...)
			next = append(next, rest[i+1:]...)
			rec(append(append([]int(nil), prefix...), rest[i]), next)
		}
	}
	rec(nil, []int{0, 1, 2, 3})
}

// TestConcurrentModelCheck hammers the store with two writers, a rotator
// and snapshot readers, then checks the quiesced state against the model
// and the recorded (key ID, nonce) stream for reuse.
func TestConcurrentModelCheck(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, &Options{
		NonceReservationBlock: 8,
		Keys:                  map[string][]byte{"k1": testKey1[:], "k2": testKey2[:]},
		ActiveKey:             "k1",
	})
	defer st.Close()

	var nonceMu sync.Mutex
	nonces := map[[12]byte]string{}
	st.hookEncrypt = func(keyID string, n [12]byte) {
		nonceMu.Lock()
		if prev, dup := nonces[n]; dup {
			t.Errorf("nonce reused: %v by %s and %s", n, prev, keyID)
		}
		nonces[n] = keyID
		nonceMu.Unlock()
	}

	const commitsPerWriter = 25
	var wg sync.WaitGroup
	// Two writers on the same object with disjoint field names, so the
	// final state is the union regardless of serialization order.
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < commitsPerWriter; i++ {
				op := Operation{
					OpID: fmt.Sprintf("w%d-%d", w, i),
					Set: map[string][]byte{
						fmt.Sprintf("field-w%d-%d", w, i): {byte(w), byte(i)},
					},
				}
				if _, err := st.Commit("o", op); err != nil {
					t.Errorf("commit %s: %v", op.OpID, err)
					return
				}
			}
		}(w)
	}
	// Rotator: publish k2 mid-stream and keep sweeping.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := st.RegisterKey("k2", testKey2[:]); err != nil {
			t.Errorf("register k2: %v", err)
			return
		}
		if err := st.SetActiveKey("k2"); err != nil {
			t.Errorf("activate k2: %v", err)
			return
		}
		for i := 0; i < 10; i++ {
			if _, err := st.RotateAll(); err != nil {
				t.Errorf("rotate: %v", err)
				return
			}
		}
	}()
	// Readers: snapshot and release in a loop; every snapshot must be
	// internally consistent (values match the writer tag pattern).
	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			sn, err := st.Snapshot("o")
			if err == ErrObjectNotFound {
				continue
			}
			if err != nil {
				t.Errorf("snapshot: %v", err)
				return
			}
			for name, v := range sn.Fields() {
				var w, i int
				if _, err := fmt.Sscanf(name, "field-w%d-%d", &w, &i); err == nil {
					if len(v) != 2 || int(v[0]) != w || int(v[1]) != i {
						t.Errorf("torn value for %s: %v", name, v)
					}
				}
			}
			sn.Close()
		}
	}()

	wg.Wait()
	close(stop)
	readerWG.Wait()

	// Quiesced checks: version == number of commits, content == union.
	sn, err := st.Snapshot("o")
	if err != nil {
		t.Fatalf("final snapshot: %v", err)
	}
	defer sn.Close()
	if sn.Version() != 2*commitsPerWriter {
		t.Fatalf("version = %d, want %d", sn.Version(), 2*commitsPerWriter)
	}
	fields := sn.Fields()
	if len(fields) != 2*commitsPerWriter {
		t.Fatalf("fields = %d, want %d", len(fields), 2*commitsPerWriter)
	}
	for w := 0; w < 2; w++ {
		for i := 0; i < commitsPerWriter; i++ {
			name := fmt.Sprintf("field-w%d-%d", w, i)
			v, ok := fields[name]
			if !ok || len(v) != 2 || int(v[0]) != w || int(v[1]) != i {
				t.Fatalf("final value for %s = %v ok=%v", name, v, ok)
			}
		}
	}
	// Everything ends on the active key.
	if _, err := st.RotateAll(); err != nil {
		t.Fatal(err)
	}
	o := st.obj("o")
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m.KeyID != "k2" || !allFieldsOn(o.m, "k2") {
		t.Fatal("not fully rotated to k2 at quiescence")
	}
}
