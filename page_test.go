package slottedpage

import (
	"bytes"
	"fmt"
	"testing"
)

func fill(b []byte, seed byte) {
	for i := range b {
		b[i] = seed + byte(i%13)
	}
}

func checkInvariant(t *testing.T, p *Page) {
	t.Helper()
	st := p.Stats()
	total := st.HeaderBytes + st.DirectoryBytes + st.RecordBytes + st.FreeBytes + st.FragmentedBytes
	if total != st.PageSize {
		t.Fatalf("space invariant broken: %d != page size %d (%+v)", total, st.PageSize, st)
	}
}

// roundTrip serializes the page and independently decodes it, then checks
// that stats and every handle's payload survive the round trip.
func roundTrip(t *testing.T, p *Page, handles []Handle, want [][]byte) *Page {
	t.Helper()
	img := p.Marshal()
	if len(img) != p.Stats().PageSize {
		t.Fatalf("serialized size %d != page size %d", len(img), p.Stats().PageSize)
	}
	q, err := Unmarshal(img)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, want := q.Stats(), p.Stats(); got != want {
		t.Fatalf("stats differ after round trip:\n got %+v\nwant %+v", got, want)
	}
	for i, h := range handles {
		got, err := q.Get(h)
		if err != nil {
			t.Fatalf("decoded page rejects live handle %v: %v", h, err)
		}
		if !bytes.Equal(got, want[i]) {
			t.Fatalf("handle %v payload mismatch after round trip", h)
		}
	}
	return q
}

func TestInsertGetAndStats(t *testing.T) {
	p, err := NewPage(7, 512)
	if err != nil {
		t.Fatal(err)
	}
	checkInvariant(t, p)

	var hs []Handle
	var data [][]byte
	for i := 0; i < 5; i++ {
		d := make([]byte, 20+i*10)
		fill(d, byte(i))
		h, err := p.Insert(d)
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		if h.PageID != 7 || h.Slot != uint16(i) || h.Gen != 1 {
			t.Fatalf("unexpected handle %+v", h)
		}
		hs = append(hs, h)
		data = append(data, d)
		checkInvariant(t, p)
	}
	for i, h := range hs {
		got, err := p.Get(h)
		if err != nil || !bytes.Equal(got, data[i]) {
			t.Fatalf("get %d: err=%v equal=%v", i, err, bytes.Equal(got, data[i]))
		}
	}
	st := p.Stats()
	if st.DirectoryBytes != 5*12 || st.RecordBytes != 20+30+40+50+60 {
		t.Fatalf("bad stats %+v", st)
	}
	roundTrip(t, p, hs, data)
}

func TestDeleteStaleHandleAndSlotReuse(t *testing.T) {
	p, _ := NewPage(1, 256)
	a, _ := p.Insert([]byte("alpha"))
	b, _ := p.Insert([]byte("beta"))
	if err := p.Delete(a); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Get(a); err != ErrStaleHandle {
		t.Fatalf("deleted handle: got %v, want ErrStaleHandle", err)
	}
	if err := p.Delete(a); err != ErrStaleHandle {
		t.Fatalf("double delete: got %v, want ErrStaleHandle", err)
	}
	// Reinsert: the tombstoned slot 0 is reused with a bumped generation.
	c, err := p.Insert([]byte("gamma"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Slot != a.Slot || c.Gen == a.Gen {
		t.Fatalf("slot reuse should keep slot, bump gen: %+v vs %+v", c, a)
	}
	if _, err := p.Get(a); err != ErrStaleHandle {
		t.Fatalf("old handle after reuse: got %v, want ErrStaleHandle", err)
	}
	if got, _ := p.Get(c); string(got) != "gamma" {
		t.Fatalf("reused slot payload %q", got)
	}
	if got, _ := p.Get(b); string(got) != "beta" {
		t.Fatalf("unrelated record corrupted: %q", got)
	}
	st := p.Stats()
	if st.TombstoneSlots != 0 || st.LiveSlots != 2 {
		t.Fatalf("slot accounting %+v", st)
	}
	roundTrip(t, p, []Handle{b, c}, [][]byte{[]byte("beta"), []byte("gamma")})
}

func TestUpdateGrowthAndAtomicFailure(t *testing.T) {
	p, _ := NewPage(2, 256)
	small := []byte("small")
	h, _ := p.Insert(small)

	// Grow the record in place.
	big := make([]byte, 100)
	fill(big, 9)
	if err := p.Update(h, big); err != nil {
		t.Fatalf("grow: %v", err)
	}
	if got, _ := p.Get(h); !bytes.Equal(got, big) {
		t.Fatal("grown record mismatch")
	}
	checkInvariant(t, p)

	// Shrink it back; the freed tail becomes fragmentation.
	if err := p.Update(h, small); err != nil {
		t.Fatal(err)
	}
	st := p.Stats()
	if st.FragmentedBytes != 105 { // 5 from the grow + 100 from the shrink
		t.Fatalf("shrink should fragment 105 bytes, got %+v", st)
	}

	// An update that cannot fit even after compaction must fail and leave
	// the page byte-for-byte identical.
	before := p.Marshal()
	huge := make([]byte, 300)
	if err := p.Update(h, huge); err != ErrPageFull {
		t.Fatalf("oversized update: got %v, want ErrPageFull", err)
	}
	if !bytes.Equal(p.Marshal(), before) {
		t.Fatal("failed update mutated the page")
	}
	if got, _ := p.Get(h); !bytes.Equal(got, small) {
		t.Fatal("record changed after failed update")
	}
}

func TestCompactionMergesFragmentsKeepsHandles(t *testing.T) {
	p, _ := NewPage(3, 256)
	mk := func(seed byte, n int) []byte { b := make([]byte, n); fill(b, seed); return b }
	ha, _ := p.Insert(mk(1, 40))
	hb, _ := p.Insert(mk(2, 40))
	hc, _ := p.Insert(mk(3, 40))

	// Delete the middle record and shrink the last one to make fragments.
	if err := p.Delete(hb); err != nil {
		t.Fatal(err)
	}
	if err := p.Update(hc, mk(3, 10)); err != nil {
		t.Fatal(err)
	}
	st := p.Stats()
	if st.FragmentedBytes != 80 { // 40 from delete + 40 from the replaced record
		t.Fatalf("expected 80 fragmented bytes, got %+v", st)
	}
	freeBefore := st.FreeBytes

	// Serialize, compact, serialize again: both images must decode
	// independently and agree on live data, while fragments merge to zero.
	imgBefore := p.Marshal()
	p.Compact()
	imgAfter := p.Marshal()

	qb, err := Unmarshal(imgBefore)
	if err != nil {
		t.Fatal(err)
	}
	qa, err := Unmarshal(imgAfter)
	if err != nil {
		t.Fatal(err)
	}
	if got := qa.Stats().FragmentedBytes; got != 0 {
		t.Fatalf("compaction left %d fragmented bytes", got)
	}
	if got := qa.Stats().FreeBytes; got != freeBefore+80 {
		t.Fatalf("compaction should merge 80 bytes into free: before %d after %d", freeBefore+80, got)
	}
	for _, q := range []*Page{qb, qa} {
		a, err := q.Get(ha)
		if err != nil || !bytes.Equal(a, mk(1, 40)) {
			t.Fatalf("handle A broken after compaction: %v", err)
		}
		c, err := q.Get(hc)
		if err != nil || !bytes.Equal(c, mk(3, 10)) {
			t.Fatalf("handle C broken after compaction: %v", err)
		}
		if _, err := q.Get(hb); err != ErrStaleHandle {
			t.Fatalf("deleted handle should stay stale: %v", err)
		}
	}
	checkInvariant(t, p)
}

func TestEmptyRecords(t *testing.T) {
	p, _ := NewPage(4, 256)
	h, err := p.Insert(nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(h)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty record: len=%d err=%v", len(got), err)
	}
	// Grow an empty record, then shrink it back to empty.
	if err := p.Update(h, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := p.Update(h, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Get(h); len(got) != 0 {
		t.Fatalf("record should be empty again, got %q", got)
	}
	// Empty records must survive compaction and serialization.
	p.Compact()
	q := roundTrip(t, p, []Handle{h}, [][]byte{{}})
	if got, _ := q.Get(h); len(got) != 0 {
		t.Fatal("empty record lost in round trip")
	}
	if err := q.Delete(h); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Get(h); err != ErrStaleHandle {
		t.Fatalf("deleted empty record: %v", err)
	}
}

func TestMaxPageBoundary(t *testing.T) {
	const size = 256
	p, _ := NewPage(5, size)
	// Directory costs 12 bytes for the first slot; the largest first record
	// is therefore 256 - 32 (header) - 12 (slot) = 212 bytes.
	exact := make([]byte, 212)
	fill(exact, 4)
	h, err := p.Insert(exact)
	if err != nil {
		t.Fatalf("exact-fit insert: %v", err)
	}
	st := p.Stats()
	if st.FreeBytes != 0 {
		t.Fatalf("page should be exactly full, %+v", st)
	}
	// One byte more anywhere must fail and change nothing.
	before := p.Marshal()
	if _, err := p.Insert([]byte("x")); err != ErrPageFull {
		t.Fatalf("insert on full page: %v", err)
	}
	if err := p.Update(h, make([]byte, 213)); err != ErrPageFull {
		t.Fatalf("grow past boundary: %v", err)
	}
	if !bytes.Equal(p.Marshal(), before) {
		t.Fatal("boundary failures mutated the page")
	}
	// Same-size update still fits exactly.
	again := make([]byte, 212)
	fill(again, 8)
	if err := p.Update(h, again); err != nil {
		t.Fatalf("same-size update on full page: %v", err)
	}
	q := roundTrip(t, p, []Handle{h}, [][]byte{again})
	if st := q.Stats(); st.FreeBytes != 0 || st.FragmentedBytes != 0 {
		t.Fatalf("decoded boundary page: %+v", st)
	}
	// Delete frees exactly enough for one max-size record again.
	if err := p.Delete(h); err != nil {
		t.Fatal(err)
	}
	h2, err := p.Insert(exact)
	if err != nil {
		t.Fatalf("refill after delete: %v", err)
	}
	if h2.Slot != h.Slot || h2.Gen <= h.Gen {
		t.Fatalf("expected slot reuse with bumped gen: %+v", h2)
	}
}

func TestUnmarshalRejectsCorruption(t *testing.T) {
	p, _ := NewPage(9, 256)
	if _, err := p.Insert([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	good := p.Marshal()
	if _, err := Unmarshal(good); err != nil {
		t.Fatalf("good image rejected: %v", err)
	}
	cases := map[string]func([]byte){
		"truncated":   func(b []byte) { /* handled by length below */ },
		"bad magic":   func(b []byte) { b[0] ^= 0xff },
		"bad size":    func(b []byte) { b[12] ^= 0xff },
		"bad freeEnd": func(b []byte) { b[20] = 0 },
	}
	for name, corrupt := range cases {
		img := append([]byte(nil), good...)
		corrupt(img)
		if name == "truncated" {
			img = img[:len(img)-1]
		}
		if _, err := Unmarshal(img); err == nil {
			t.Fatalf("%s: corruption not detected", name)
		}
	}
}

func TestManyRandomOpsKeepInvariant(t *testing.T) {
	p, _ := NewPage(11, 1024)
	var live []Handle
	seed := byte(0)
	for i := 0; i < 200; i++ {
		seed += 7
		switch {
		case len(live) == 0 || i%3 == 0:
			d := make([]byte, int(seed)%60)
			fill(d, seed)
			if h, err := p.Insert(d); err == nil {
				live = append(live, h)
			}
		case i%3 == 1:
			h := live[0]
			live = live[1:]
			_ = p.Delete(h)
		default:
			h := live[0]
			d := make([]byte, int(seed)%80)
			fill(d, seed)
			_ = p.Update(h, d)
		}
		checkInvariant(t, p)
		if i%25 == 0 {
			p.Compact()
			checkInvariant(t, p)
		}
	}
	// Every surviving handle must decode identically from the image.
	img := p.Marshal()
	q, err := Unmarshal(img)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range live {
		a, errA := p.Get(h)
		b, errB := q.Get(h)
		if fmt.Sprint(errA) != fmt.Sprint(errB) || !bytes.Equal(a, b) {
			t.Fatalf("handle %+v diverged after round trip", h)
		}
	}
}
