package slottedpage

import (
	"bytes"
	"testing"
)

func fill(b []byte, seed byte) {
	for i := range b {
		b[i] = seed + byte(i%13)
	}
}

func mustInsert(t *testing.T, p *Page, rec []byte) Handle {
	t.Helper()
	h, err := p.Insert(rec)
	if err != nil {
		t.Fatalf("Insert(len=%d) failed: %v", len(rec), err)
	}
	return h
}

func mustGet(t *testing.T, p *Page, h Handle) []byte {
	t.Helper()
	rec, err := p.Get(h)
	if err != nil {
		t.Fatalf("Get(%+v) failed: %v", h, err)
	}
	return rec
}

// TestFragmentMerging deletes records to create fragmentation, then checks
// that Compact merges it into contiguous free space without changing any
// live handle, and that the accounting survives a serialize round-trip.
func TestFragmentMerging(t *testing.T) {
	p := NewPageSize(7, 512)
	recs := make([][]byte, 5)
	hs := make([]Handle, 5)
	for i := range recs {
		recs[i] = make([]byte, 40)
		fill(recs[i], byte(i*20))
		hs[i] = mustInsert(t, p, recs[i])
	}
	// Punch holes: delete records 1 and 3.
	for _, i := range []int{1, 3} {
		if err := p.Delete(hs[i]); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
	}
	st := p.Stats()
	if st.FragmentedBytes != 80 {
		t.Fatalf("want 80 fragmented bytes, got %d (%+v)", st.FragmentedBytes, st)
	}
	if st.ContiguousFree+st.FragmentedBytes != st.FreeBytes {
		t.Fatalf("free space does not add up: %+v", st)
	}

	p.Compact()
	st = p.Stats()
	if st.FragmentedBytes != 0 {
		t.Fatalf("compaction left %d fragmented bytes", st.FragmentedBytes)
	}
	if st.FreeBytes != st.ContiguousFree {
		t.Fatalf("free space not fully contiguous after compact: %+v", st)
	}
	// Handles survive compaction untouched.
	for _, i := range []int{0, 2, 4} {
		if got := mustGet(t, p, hs[i]); !bytes.Equal(got, recs[i]) {
			t.Fatalf("record %d corrupted by compaction", i)
		}
	}

	// Round-trip: independently decoded page must agree on stats and data.
	q := mustDeserialize(t, p.Serialize())
	if got, want := q.Stats(), p.Stats(); got != want {
		t.Fatalf("stats differ after round-trip:\n got %+v\nwant %+v", got, want)
	}
	for _, i := range []int{0, 2, 4} {
		if got := mustGet(t, q, hs[i]); !bytes.Equal(got, recs[i]) {
			t.Fatalf("record %d corrupted by round-trip", i)
		}
	}
}

// TestEmptyRecords exercises zero-length records through the full lifecycle.
func TestEmptyRecords(t *testing.T) {
	p := NewPageSize(1, 256)
	h := mustInsert(t, p, nil)
	if got := mustGet(t, p, h); len(got) != 0 {
		t.Fatalf("empty record read back as %d bytes", len(got))
	}
	// Grow from empty, shrink back to empty.
	grown := []byte("now with content")
	if err := p.Update(h, grown); err != nil {
		t.Fatalf("grow from empty failed: %v", err)
	}
	if got := mustGet(t, p, h); !bytes.Equal(got, grown) {
		t.Fatalf("grown record mismatch")
	}
	if err := p.Update(h, []byte{}); err != nil {
		t.Fatalf("shrink to empty failed: %v", err)
	}
	if got := mustGet(t, p, h); got == nil || len(got) != 0 {
		t.Fatalf("record not empty after shrink: %v", got)
	}
	// Empty records survive serialization.
	q := mustDeserialize(t, p.Serialize())
	if got := mustGet(t, q, h); len(got) != 0 {
		t.Fatalf("empty record not empty after round-trip")
	}
	if err := p.Delete(h); err != nil {
		t.Fatalf("delete empty record failed: %v", err)
	}
	if _, err := p.Get(h); err != ErrStaleHandle {
		t.Fatalf("deleted empty record: want ErrStaleHandle, got %v", err)
	}
}

// TestMaxPageBoundary inserts a record of exactly MaxRecordSize, verifies
// that any growth fails atomically, and that shrink+regrow to the exact
// boundary works.
func TestMaxPageBoundary(t *testing.T) {
	p := NewPageSize(9, 256)
	max := p.MaxRecordSize() // 256 - 16 - 8 = 232
	rec := make([]byte, max)
	fill(rec, 0xA0)
	h := mustInsert(t, p, rec)

	st := p.Stats()
	if st.FreeBytes != 0 || st.ContiguousFree != 0 {
		t.Fatalf("page should be exactly full: %+v", st)
	}
	if _, err := p.Insert([]byte{1}); err != ErrNoSpace {
		t.Fatalf("insert into full page: want ErrNoSpace, got %v", err)
	}

	// Growth by one byte must fail and leave the page bit-for-bit identical.
	before := p.Serialize()
	tooBig := make([]byte, max+1)
	if err := p.Update(h, tooBig); err == nil {
		t.Fatalf("oversized update unexpectedly succeeded")
	}
	if !bytes.Equal(p.Serialize(), before) {
		t.Fatalf("failed update mutated the page")
	}
	if got := mustGet(t, p, h); !bytes.Equal(got, rec) {
		t.Fatalf("record changed after failed update")
	}

	// Shrink then regrow to the exact boundary.
	if err := p.Update(h, rec[:100]); err != nil {
		t.Fatalf("shrink failed: %v", err)
	}
	if err := p.Update(h, rec); err != nil {
		t.Fatalf("regrow to max failed: %v", err)
	}
	if got := mustGet(t, p, h); !bytes.Equal(got, rec) {
		t.Fatalf("record mismatch after regrow")
	}
	if st := p.Stats(); st.FreeBytes != 0 {
		t.Fatalf("page should be full again: %+v", st)
	}
	// A record larger than MaxRecordSize can never be inserted.
	if _, err := NewPageSize(9, 256).Insert(make([]byte, max+1)); err != ErrRecordTooLarge {
		t.Fatalf("oversized insert: want ErrRecordTooLarge, got %v", err)
	}
}

// TestSlotReuseInvalidatesOldHandles deletes a record, reuses its slot via
// Insert, and checks that the old handle is stale everywhere.
func TestSlotReuseInvalidatesOldHandles(t *testing.T) {
	p := NewPageSize(3, 256)
	h1 := mustInsert(t, p, []byte("first incarnation"))
	if err := p.Delete(h1); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	// Deleting twice with the same handle must fail.
	if err := p.Delete(h1); err != ErrStaleHandle {
		t.Fatalf("double delete: want ErrStaleHandle, got %v", err)
	}
	h2 := mustInsert(t, p, []byte("second incarnation"))
	if h2.Slot != h1.Slot {
		t.Fatalf("slot not reused: %d -> %d", h1.Slot, h2.Slot)
	}
	if h2.Generation == h1.Generation {
		t.Fatalf("generation not bumped on reuse")
	}
	if _, err := p.Get(h1); err != ErrStaleHandle {
		t.Fatalf("old handle Get: want ErrStaleHandle, got %v", err)
	}
	if err := p.Update(h1, []byte("zombie")); err != ErrStaleHandle {
		t.Fatalf("old handle Update: want ErrStaleHandle, got %v", err)
	}
	if err := p.Delete(h1); err != ErrStaleHandle {
		t.Fatalf("old handle Delete: want ErrStaleHandle, got %v", err)
	}
	if got := mustGet(t, p, h2); string(got) != "second incarnation" {
		t.Fatalf("new handle reads %q", got)
	}
	// Handles from another page ID are rejected.
	alien := h2
	alien.PageID++
	if _, err := p.Get(alien); err != ErrStaleHandle {
		t.Fatalf("alien handle: want ErrStaleHandle, got %v", err)
	}
}

// TestFailedUpdateLeavesPageUnchanged fills a page almost completely, then
// attempts an update that only fits if fragmentation were counted twice.
func TestFailedUpdateLeavesPageUnchanged(t *testing.T) {
	p := NewPageSize(4, 256)
	a := mustInsert(t, p, make([]byte, 80))
	b := mustInsert(t, p, make([]byte, 80))
	mustInsert(t, p, make([]byte, 56))  // 16 header + 24 dir + 216 records = 256
	if err := p.Delete(b); err != nil { // 80 fragmented bytes, 0 contiguous
		t.Fatalf("Delete failed: %v", err)
	}
	st := p.Stats()
	if st.FragmentedBytes != 80 || st.ContiguousFree != 0 {
		t.Fatalf("unexpected layout: %+v", st)
	}
	before := p.Serialize()
	// Needs 81 more bytes; only 80 reclaimable. Must fail atomically.
	if err := p.Update(a, make([]byte, 161)); err != ErrNoSpace {
		t.Fatalf("want ErrNoSpace, got %v", err)
	}
	if !bytes.Equal(p.Serialize(), before) {
		t.Fatalf("page image changed after failed update")
	}
	// Exactly 80 more bytes fits (via compaction) and keeps the handle.
	rec := make([]byte, 160)
	fill(rec, 0x33)
	if err := p.Update(a, rec); err != nil {
		t.Fatalf("boundary update failed: %v", err)
	}
	if got := mustGet(t, p, a); !bytes.Equal(got, rec) {
		t.Fatalf("record mismatch after boundary update")
	}
	if st := p.Stats(); st.FreeBytes != 0 || st.FragmentedBytes != 0 {
		t.Fatalf("page should be exactly full and compact: %+v", st)
	}
}

// TestSerializationRoundTrip builds a page with live records, empty
// records, and tombstones, then checks that an independently decoded page
// agrees on every record, every stat, and the full serialized image.
func TestSerializationRoundTrip(t *testing.T) {
	p := NewPageSize(42, 1024)
	type entry struct {
		h   Handle
		rec []byte
	}
	var live []entry
	for i, n := range []int{0, 1, 100, 0, 255, 37} {
		rec := make([]byte, n)
		fill(rec, byte(i))
		h := mustInsert(t, p, rec)
		live = append(live, entry{h, rec})
	}
	// Delete some to create tombstones, then reuse one slot.
	for _, i := range []int{1, 3} {
		if err := p.Delete(live[i].h); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
	}
	reused := mustInsert(t, p, []byte("slot reuse"))
	live[1] = entry{reused, []byte("slot reuse")}
	live = append(live[:3], live[4:]...) // drop entry 3 (still a tombstone)

	img1 := p.Serialize()
	q := mustDeserialize(t, img1)
	if got, want := q.Stats(), p.Stats(); got != want {
		t.Fatalf("stats differ:\n got %+v\nwant %+v", got, want)
	}
	for _, e := range live {
		if got := mustGet(t, q, e.h); !bytes.Equal(got, e.rec) {
			t.Fatalf("record mismatch after round-trip (slot %d)", e.h.Slot)
		}
	}
	// Tombstone generations survive: stale handles stay stale.
	stale := Handle{PageID: 42, Slot: 3, Generation: 1}
	if _, err := q.Get(stale); err != ErrStaleHandle {
		t.Fatalf("stale handle revived after round-trip: %v", err)
	}
	// Serializing the decoded page reproduces the identical image.
	if img2 := q.Serialize(); !bytes.Equal(img1, img2) {
		t.Fatalf("re-serialized image differs")
	}
}

// TestDeserializeRejectsGarbage sanity-checks the decoder.
func TestDeserializeRejectsGarbage(t *testing.T) {
	if _, err := Deserialize(nil); err != ErrBadPage {
		t.Fatalf("nil image: want ErrBadPage, got %v", err)
	}
	img := NewPageSize(1, 256).Serialize()
	img[0] ^= 0xFF // corrupt magic
	if _, err := Deserialize(img); err != ErrBadPage {
		t.Fatalf("bad magic: want ErrBadPage, got %v", err)
	}
}

func mustDeserialize(t *testing.T, img []byte) *Page {
	t.Helper()
	p, err := Deserialize(img)
	if err != nil {
		t.Fatalf("Deserialize failed: %v", err)
	}
	return p
}
