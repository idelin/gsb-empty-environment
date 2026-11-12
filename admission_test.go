package taskpkg

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time          { return f.now }
func (f *fakeClock) Advance(d time.Duration) { f.now = f.now.Add(d) }

var generous = Limits{MaxWaitingSections: 16, MaxWaitingBytes: 1 << 20, MaxWait: time.Minute}

func newHarness(capacity int, limits Limits) (*Conn, *fakeClock) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	return NewConn(NewDynamicTable(capacity), limits, clk.Now), clk
}

func mustInsert(t *testing.T, c *Conn, pairs ...[2]string) {
	t.Helper()
	for _, p := range pairs {
		if _, err := c.Insert(p[0], p[1]); err != nil {
			t.Fatalf("Insert(%q): %v", p[0], err)
		}
	}
}

func mustAdmit(t *testing.T, c *Conn, sec Section, want Outcome) {
	t.Helper()
	got, err := c.Admit(sec)
	if err != nil {
		t.Fatalf("Admit(%d): %v", sec.ID, err)
	}
	if got != want {
		t.Fatalf("Admit(%d) = %v, want %v", sec.ID, got, want)
	}
}

// Two sections legally share the same dynamic entries and both complete.
func TestSharedReferencesComplete(t *testing.T) {
	c, _ := newHarness(4, generous)
	mustInsert(t, c, [2]string{"a", "1"}, [2]string{"b", "2"})

	mustAdmit(t, c, Section{ID: 1, Gen: 0, RequiredInsertCount: 2, Refs: []uint64{0, 1}, EncodedLen: 16}, Ready)
	mustAdmit(t, c, Section{ID: 2, Gen: 0, RequiredInsertCount: 2, Refs: []uint64{0, 1}, EncodedLen: 16}, Ready)

	for _, id := range []uint64{1, 2} {
		entries, err := c.Decode(id)
		if err != nil {
			t.Fatalf("Decode(%d): %v", id, err)
		}
		if len(entries) != 2 || entries[0].Name != "a" || entries[1].Name != "b" {
			t.Fatalf("Decode(%d) entries = %+v", id, entries)
		}
	}
	if got := c.Delivered(); !reflect.DeepEqual(got, []uint64{1, 2}) {
		t.Fatalf("Delivered = %v", got)
	}
	if c.Terminated() {
		t.Fatal("connection must stay alive")
	}
}

// A section declaring a far-future required insert count can never be
// unblocked; the wait deadline caps how long it may hold resources.
func TestFarFutureIndexTimesOut(t *testing.T) {
	limits := Limits{MaxWaitingSections: 8, MaxWaitingBytes: 1 << 20, MaxWait: 5 * time.Second}
	c, clk := newHarness(4, limits)

	far := uint64(1) << 60
	mustAdmit(t, c, Section{ID: 1, Gen: 0, RequiredInsertCount: far, Refs: []uint64{far - 1}, EncodedLen: 32}, Waiting)
	if c.WaitingSections() != 1 || c.WaitingBytes() != 32 {
		t.Fatalf("waiting = %d sections / %d bytes", c.WaitingSections(), c.WaitingBytes())
	}
	if _, err := c.Decode(1); !errors.Is(err, ErrSectionBlocked) {
		t.Fatalf("Decode while blocked: %v", err)
	}

	clk.Advance(4 * time.Second)
	c.Tick()
	if c.Terminated() {
		t.Fatal("terminated before deadline")
	}

	clk.Advance(2 * time.Second)
	c.Tick()
	if !c.Terminated() {
		t.Fatal("not terminated after deadline")
	}
	if !errors.Is(c.Err(), ErrWaitTimeout) {
		t.Fatalf("Err = %v", c.Err())
	}
	if got := c.Delivered(); len(got) != 0 {
		t.Fatalf("Delivered = %v, want zero delivery", got)
	}
	if _, err := c.Admit(Section{ID: 2}); !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("Admit after termination: %v", err)
	}
}

// The waiting-section count cap is enforced independently.
func TestWaitingSectionLimit(t *testing.T) {
	limits := Limits{MaxWaitingSections: 1, MaxWaitingBytes: 1 << 20, MaxWait: time.Minute}
	c, _ := newHarness(4, limits)

	mustAdmit(t, c, Section{ID: 1, Gen: 0, RequiredInsertCount: 10, EncodedLen: 8}, Waiting)
	if _, err := c.Admit(Section{ID: 2, Gen: 0, RequiredInsertCount: 10, EncodedLen: 8}); !errors.Is(err, ErrWaitingLimit) {
		t.Fatalf("second waiter: %v", err)
	}
	if !c.Terminated() {
		t.Fatal("connection must terminate on limit breach")
	}
	if got := c.Delivered(); len(got) != 0 {
		t.Fatalf("Delivered = %v, want zero delivery", got)
	}
}

// The cumulative encoded-bytes cap is enforced independently.
func TestWaitingBytesLimit(t *testing.T) {
	limits := Limits{MaxWaitingSections: 8, MaxWaitingBytes: 40, MaxWait: time.Minute}
	c, _ := newHarness(4, limits)

	mustAdmit(t, c, Section{ID: 1, Gen: 0, RequiredInsertCount: 10, EncodedLen: 30}, Waiting)
	if _, err := c.Admit(Section{ID: 2, Gen: 0, RequiredInsertCount: 10, EncodedLen: 30}); !errors.Is(err, ErrWaitingBytesLimit) {
		t.Fatalf("second waiter: %v", err)
	}
	if !errors.Is(c.Err(), ErrWaitingBytesLimit) || !c.Terminated() {
		t.Fatalf("Err = %v, terminated = %v", c.Err(), c.Terminated())
	}
}

// A cancel storm against one section must not release references still
// held by another section sharing the same entries.
func TestCancelStormKeepsSharedPins(t *testing.T) {
	c, _ := newHarness(2, generous)
	mustInsert(t, c, [2]string{"a", "1"}, [2]string{"b", "2"})

	mustAdmit(t, c, Section{ID: 1, Gen: 0, RequiredInsertCount: 6, Refs: []uint64{0, 5}, EncodedLen: 10}, Waiting)
	mustAdmit(t, c, Section{ID: 2, Gen: 0, RequiredInsertCount: 6, Refs: []uint64{0, 5}, EncodedLen: 10}, Waiting)
	if got := c.table.PinCount(0); got != 2 {
		t.Fatalf("PinCount(0) = %d, want 2", got)
	}

	for i := 0; i < 1000; i++ {
		c.Cancel(1)   // repeated cancels of the same section
		c.Cancel(999) // unknown ids are no-ops
	}
	if c.Terminated() {
		t.Fatal("cancel storm terminated the connection")
	}
	if got := c.table.PinCount(0); got != 1 {
		t.Fatalf("PinCount(0) = %d, want 1 (section 2 still holds it)", got)
	}
	if c.WaitingSections() != 1 || c.WaitingBytes() != 10 {
		t.Fatalf("waiting = %d sections / %d bytes", c.WaitingSections(), c.WaitingBytes())
	}

	// Capacity is 2; entry 0 survives only because section 2's pin was
	// not released by section 1's cancellation.
	mustInsert(t, c, [2]string{"c", "3"}, [2]string{"d", "4"}, [2]string{"e", "5"}, [2]string{"f", "6"})
	if !c.table.Live(0) {
		t.Fatal("shared entry 0 evicted despite section 2's pin")
	}
	if c.WaitingSections() != 0 {
		t.Fatal("section 2 should have been promoted")
	}

	entries, err := c.Decode(2)
	if err != nil {
		t.Fatalf("Decode(2): %v", err)
	}
	if len(entries) != 2 || entries[0].Name != "a" || entries[1].Name != "f" {
		t.Fatalf("Decode(2) entries = %+v", entries)
	}
	if _, err := c.Decode(1); !errors.Is(err, ErrUnknownSection) {
		t.Fatalf("Decode(1) after cancel: %v", err)
	}
	if got := c.Delivered(); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("Delivered = %v", got)
	}
}

// A ready section holds no pins; if its entry is evicted before decode,
// the reference is illegal and the connection terminates with zero
// delivery.
func TestEvictedReferenceTerminates(t *testing.T) {
	c, _ := newHarness(2, generous)
	mustInsert(t, c, [2]string{"a", "1"}, [2]string{"b", "2"})
	mustAdmit(t, c, Section{ID: 1, Gen: 0, RequiredInsertCount: 2, Refs: []uint64{1, 0}, EncodedLen: 8}, Ready)

	mustInsert(t, c, [2]string{"c", "3"}) // evicts entry 0

	entries, err := c.Decode(1)
	if !errors.Is(err, ErrIllegalReference) {
		t.Fatalf("Decode = %v", err)
	}
	if entries != nil {
		t.Fatalf("Decode delivered %v on illegal reference", entries)
	}
	if !c.Terminated() {
		t.Fatal("connection must terminate on illegal reference")
	}
	if got := c.Delivered(); len(got) != 0 {
		t.Fatalf("Delivered = %v, want zero delivery", got)
	}
	if _, err := c.Insert("d", "4"); !errors.Is(err, ErrIllegalReference) {
		t.Fatalf("Insert after termination: %v", err)
	}
}

// A reference at or beyond the declared required insert count is
// inconsistent and terminates the connection at admission.
func TestRefBeyondDeclaredCount(t *testing.T) {
	c, _ := newHarness(4, generous)
	if _, err := c.Admit(Section{ID: 1, Gen: 0, RequiredInsertCount: 1, Refs: []uint64{5}}); !errors.Is(err, ErrIllegalReference) {
		t.Fatalf("Admit = %v", err)
	}
	if !c.Terminated() {
		t.Fatal("connection must terminate")
	}
}

// Entries belong to a connection generation; after reissue, stale
// generations are rejected and the new generation works normally.
func TestGenerationReissue(t *testing.T) {
	c, _ := newHarness(4, generous)
	mustInsert(t, c, [2]string{"a", "1"})
	mustAdmit(t, c, Section{ID: 1, Gen: 0, RequiredInsertCount: 1, Refs: []uint64{0}, EncodedLen: 8}, Ready)

	c.Reissue()
	if _, err := c.Decode(1); !errors.Is(err, ErrUnknownSection) {
		t.Fatalf("Decode stale section: %v", err)
	}
	if _, err := c.Admit(Section{ID: 2, Gen: 0, RequiredInsertCount: 0}); !errors.Is(err, ErrIllegalReference) {
		t.Fatalf("Admit stale gen: %v", err)
	}
	if !c.Terminated() {
		t.Fatal("stale generation must terminate the connection")
	}

	// A fresh connection on the reissued table accepts the new generation.
	c2 := NewConn(c.table, generous, nil)
	mustInsert(t, c2, [2]string{"x", "9"})
	mustAdmit(t, c2, Section{ID: 1, Gen: 1, RequiredInsertCount: 1, Refs: []uint64{0}, EncodedLen: 8}, Ready)
	entries, err := c2.Decode(1)
	if err != nil {
		t.Fatalf("Decode new gen: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "x" || entries[0].Gen != 1 {
		t.Fatalf("entries = %+v", entries)
	}
}

// Admitting the same section id twice is a protocol error.
func TestDuplicateSection(t *testing.T) {
	c, _ := newHarness(4, generous)
	mustAdmit(t, c, Section{ID: 1, Gen: 0, RequiredInsertCount: 0}, Ready)
	if _, err := c.Admit(Section{ID: 1, Gen: 0, RequiredInsertCount: 0}); !errors.Is(err, ErrDuplicateSection) {
		t.Fatalf("duplicate Admit = %v", err)
	}
	if !c.Terminated() {
		t.Fatal("connection must terminate on duplicate section")
	}
}
