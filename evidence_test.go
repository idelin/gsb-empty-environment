package taskpkg

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func mustOpen(t *testing.T, s *Store, tenant string) Identity {
	t.Helper()
	id, err := s.OpenConnection(tenant)
	if err != nil {
		t.Fatalf("OpenConnection(%q): %v", tenant, err)
	}
	return id
}

func mustAdd(t *testing.T, s *Store, id Identity, offset uint64, data []byte) BlockID {
	t.Helper()
	block, err := s.Add(Fragment{Identity: id, Offset: offset, Data: data})
	if err != nil {
		t.Fatalf("Add at %d: %v", offset, err)
	}
	return block
}

func mustAssemble(t *testing.T, s *Store, alert Alert) *Evidence {
	t.Helper()
	ev, err := s.Assemble(alert)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return ev
}

func dataString(seg Segment) string {
	if seg.Kind != KindData {
		return ""
	}
	return string(seg.Data)
}

func TestAssembleClipsHalfOpenIntervalAndSortsOutOfOrder(t *testing.T) {
	s := NewStore()
	id := mustOpen(t, s, "tenant-a")

	// Deliberately add the later block first.
	b2 := mustAdd(t, s, id, 120, []byte("0123456789abcdefghij"))
	b1 := mustAdd(t, s, id, 100, []byte("abcdefghijklmnopqrst"))

	ev := mustAssemble(t, s, Alert{Identity: id, Start: 110, End: 140})
	if err := ev.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ev.Start != 110 || ev.End != 140 {
		t.Fatalf("range = [%d,%d), want [110,140)", ev.Start, ev.End)
	}

	want := []struct {
		kind SegmentKind
		val  string
		from BlockID
	}{
		{KindData, "klmnopqrst", b1},
		{KindData, "0123456789abcdefghij", b2},
	}
	if len(ev.Segments) != len(want) {
		t.Fatalf("got %d segments, want %d: %+v", len(ev.Segments), len(want), ev.Segments)
	}
	for i, w := range want {
		seg := ev.Segments[i]
		if seg.Kind != w.kind || dataString(seg) != w.val {
			t.Fatalf("segment %d = %s %q, want %s %q", i, seg.Kind, dataString(seg), w.kind, w.val)
		}
		if len(seg.Sources) != 1 || seg.Sources[0].Block != w.from {
			t.Fatalf("segment %d sources = %+v, want block %d", i, seg.Sources, w.from)
		}
		if seg.Sources[0].Start != seg.Start || seg.Sources[0].End != seg.End {
			t.Fatalf("segment %d source range = [%d,%d), want [%d,%d)",
				i, seg.Sources[0].Start, seg.Sources[0].End, seg.Start, seg.End)
		}
	}
}

func TestGapIsExplicitAndNeverFilled(t *testing.T) {
	s := NewStore()
	id := mustOpen(t, s, "tenant-a")
	mustAdd(t, s, id, 0, []byte("ABCD"))
	mustAdd(t, s, id, 8, []byte("WXYZ"))

	ev := mustAssemble(t, s, Alert{Identity: id, Start: 0, End: 12})
	if err := ev.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(ev.Segments) != 3 {
		t.Fatalf("got %d segments, want data/gap/data: %+v", len(ev.Segments), ev.Segments)
	}
	if ev.Segments[0].Kind != KindData || string(ev.Segments[0].Data) != "ABCD" {
		t.Fatalf("first segment = %+v", ev.Segments[0])
	}
	gap := ev.Segments[1]
	if gap.Kind != KindGap || gap.Start != 4 || gap.End != 8 {
		t.Fatalf("middle segment = %+v, want gap [4,8)", gap)
	}
	if len(gap.Data) != 0 || len(gap.Sources) != 0 || len(gap.Variants) != 0 {
		t.Fatalf("gap carries data or provenance: %+v", gap)
	}
	if ev.Segments[2].Kind != KindData || string(ev.Segments[2].Data) != "WXYZ" {
		t.Fatalf("last segment = %+v", ev.Segments[2])
	}
	if _, err := ev.Trace(5); !errors.Is(err, ErrGap) {
		t.Fatalf("Trace in gap = %v, want ErrGap", err)
	}
}

func TestExactIdentitySeparatesTenantsAndGenerations(t *testing.T) {
	s := NewStore()
	a1 := mustOpen(t, s, "tenant-a")
	a2 := mustOpen(t, s, "tenant-a")
	b1 := mustOpen(t, s, "tenant-b")

	blockA1 := mustAdd(t, s, a1, 0, []byte("AAAA"))
	blockA2 := mustAdd(t, s, a2, 0, []byte("CCCC"))
	blockB1 := mustAdd(t, s, b1, 0, []byte("BBBB"))

	evA1 := mustAssemble(t, s, Alert{Identity: a1, Start: 0, End: 4})
	evA2 := mustAssemble(t, s, Alert{Identity: a2, Start: 0, End: 4})
	evB1 := mustAssemble(t, s, Alert{Identity: b1, Start: 0, End: 4})

	for _, tc := range []struct {
		name string
		ev   *Evidence
		id   Identity
		blk  BlockID
		want string
	}{
		{"tenant-a generation 1", evA1, a1, blockA1, "AAAA"},
		{"tenant-a generation 2", evA2, a2, blockA2, "CCCC"},
		{"tenant-b generation 1", evB1, b1, blockB1, "BBBB"},
	} {
		if err := tc.ev.Verify(); err != nil {
			t.Fatalf("%s Verify: %v", tc.name, err)
		}
		if got := dataString(tc.ev.Segments[0]); got != tc.want {
			t.Fatalf("%s data = %q, want %q", tc.name, got, tc.want)
		}
		if tc.ev.Segments[0].Sources[0].Block != tc.blk {
			t.Fatalf("%s source = %d, want %d", tc.name, tc.ev.Segments[0].Sources[0].Block, tc.blk)
		}
		for off := uint64(0); off < 4; off++ {
			got, err := tc.ev.ByteIdentity(off)
			if err != nil {
				t.Fatalf("%s ByteIdentity(%d): %v", tc.name, off, err)
			}
			if !got.Equal(tc.id) {
				t.Fatalf("%s byte %d identity = %s, want %s", tc.name, off, got, tc.id)
			}
		}
	}
}

func TestIdenticalBytesFromAnotherIdentityAreNeverJoined(t *testing.T) {
	s := NewStore()
	a1 := mustOpen(t, s, "tenant-a")
	a2 := mustOpen(t, s, "tenant-a")
	b1 := mustOpen(t, s, "tenant-b")

	a1Block := mustAdd(t, s, a1, 10, []byte("SAME"))
	mustAdd(t, s, a2, 10, []byte("SAME"))
	mustAdd(t, s, b1, 10, []byte("SAME"))

	ev := mustAssemble(t, s, Alert{Identity: a1, Start: 10, End: 14})
	if err := ev.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(ev.Segments) != 1 || ev.Segments[0].Kind != KindData {
		t.Fatalf("segments = %+v", ev.Segments)
	}
	if len(ev.Segments[0].Sources) != 1 || ev.Segments[0].Sources[0].Block != a1Block {
		t.Fatalf("sources = %+v, want only block %d", ev.Segments[0].Sources, a1Block)
	}
}

func TestPooledBuffersCannotLeakAcrossTenants(t *testing.T) {
	s := NewStore()
	a := mustOpen(t, s, "tenant-a")
	b := mustOpen(t, s, "tenant-b")

	// A deliberately tiny pool whose backing array is overwritten repeatedly.
	pool := [][]byte{
		make([]byte, 8),
		make([]byte, 8),
	}
	buf := pool[0]
	copy(buf, "AAAA-sec")
	blockA := mustAdd(t, s, a, 0, buf)

	// Reuse the same storage for another tenant immediately.
	copy(buf, "BBBB-sec")
	blockB := mustAdd(t, s, b, 0, buf)

	// And reuse it again after both Adds, with a third secret.
	copy(buf, "CCCC-sec")

	evA := mustAssemble(t, s, Alert{Identity: a, Start: 0, End: 8})
	evB := mustAssemble(t, s, Alert{Identity: b, Start: 0, End: 8})
	if err := evA.Verify(); err != nil {
		t.Fatalf("A Verify: %v", err)
	}
	if err := evB.Verify(); err != nil {
		t.Fatalf("B Verify: %v", err)
	}
	if got := dataString(evA.Segments[0]); got != "AAAA-sec" {
		t.Fatalf("A data = %q, want AAAA-sec", got)
	}
	if got := dataString(evB.Segments[0]); got != "BBBB-sec" {
		t.Fatalf("B data = %q, want BBBB-sec", got)
	}
	if evA.Segments[0].Sources[0].Block != blockA ||
		evB.Segments[0].Sources[0].Block != blockB {
		t.Fatalf("source blocks do not match Add results")
	}

	// Output must not expose spare capacity from the pool.
	for _, ev := range []*Evidence{evA, evB} {
		if cap(ev.Segments[0].Data) != len(ev.Segments[0].Data) {
			t.Fatalf("segment spare capacity = %d for %d bytes", cap(ev.Segments[0].Data), len(ev.Segments[0].Data))
		}
		for _, src := range ev.Segments[0].Sources {
			if cap(src.Data) != len(src.Data) {
				t.Fatalf("source spare capacity = %d for %d bytes", cap(src.Data), len(src.Data))
			}
		}
		if bytes.Contains(ev.Segments[0].Data, []byte("CCCC")) {
			t.Fatalf("pending evidence contains later pool contents")
		}
	}
}

func TestConsistentOverlapJoinsButKeepsBothSources(t *testing.T) {
	s := NewStore()
	id := mustOpen(t, s, "tenant-a")
	left := mustAdd(t, s, id, 0, []byte("abcdefgh"))
	right := mustAdd(t, s, id, 4, []byte("efghijkl"))

	ev := mustAssemble(t, s, Alert{Identity: id, Start: 0, End: 12})
	if err := ev.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	var data []byte
	for _, seg := range ev.Segments {
		if seg.Kind != KindData {
			t.Fatalf("unexpected %s segment", seg.Kind)
		}
		data = append(data, seg.Data...)
	}
	if string(data) != "abcdefghijkl" {
		t.Fatalf("assembled %q, want abcdefghijkl", data)
	}

	// The [4,8) span has two agreeing contributors.
	var overlap Segment
	for _, seg := range ev.Segments {
		if seg.Start == 4 {
			overlap = seg
			break
		}
	}
	if overlap.Kind != KindData || overlap.End != 8 {
		t.Fatalf("overlap segment = %+v", overlap)
	}
	if len(overlap.Sources) != 2 ||
		overlap.Sources[0].Block != left || overlap.Sources[1].Block != right {
		t.Fatalf("overlap sources = %+v, want blocks %d and %d", overlap.Sources, left, right)
	}
}

func TestOverlapConflictReportsBothSources(t *testing.T) {
	s := NewStore()
	id := mustOpen(t, s, "tenant-a")
	left := mustAdd(t, s, id, 0, []byte("abcdefgh"))
	right := mustAdd(t, s, id, 4, []byte("WXYZijkl"))

	ev := mustAssemble(t, s, Alert{Identity: id, Start: 0, End: 12})
	if err := ev.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(ev.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(ev.Conflicts))
	}

	conflictSeg := ev.Segments[1]
	if conflictSeg.Kind != KindOverlapConflict || conflictSeg.Start != 4 || conflictSeg.End != 8 {
		t.Fatalf("conflict segment = %+v", conflictSeg)
	}
	if len(conflictSeg.Data) != 0 {
		t.Fatalf("conflict selected bytes: %q", conflictSeg.Data)
	}
	if len(conflictSeg.Sources) != 2 || len(conflictSeg.Variants) != 2 {
		t.Fatalf("conflict sources/variants = %d/%d, want 2/2", len(conflictSeg.Sources), len(conflictSeg.Variants))
	}

	got := map[string]BlockID{}
	for _, v := range conflictSeg.Variants {
		if len(v.Sources) != 1 {
			t.Fatalf("variant sources = %+v", v.Sources)
		}
		got[string(v.Data)] = v.Sources[0].Block
	}
	if got["efgh"] != left || got["WXYZ"] != right {
		t.Fatalf("variant provenance = %+v, want efgh:%d WXYZ:%d", got, left, right)
	}
	if _, err := ev.ByteIdentity(5); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("ByteIdentity in conflict = %v, want ErrEvidenceInvalid", err)
	}
}

func TestThreeWayConflictKeepsAllSources(t *testing.T) {
	s := NewStore()
	id := mustOpen(t, s, "tenant-a")
	a := mustAdd(t, s, id, 0, []byte("aa"))
	b := mustAdd(t, s, id, 0, []byte("bb"))
	c := mustAdd(t, s, id, 0, []byte("cc"))

	ev := mustAssemble(t, s, Alert{Identity: id, Start: 0, End: 2})
	if err := ev.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(ev.Segments) != 1 || ev.Segments[0].Kind != KindOverlapConflict {
		t.Fatalf("segments = %+v", ev.Segments)
	}
	seg := ev.Segments[0]
	if len(seg.Variants) != 3 || len(seg.Sources) != 3 {
		t.Fatalf("variants/sources = %d/%d, want 3/3", len(seg.Variants), len(seg.Sources))
	}
	blockSet := map[BlockID]bool{}
	for _, src := range seg.Sources {
		blockSet[src.Block] = true
	}
	for _, want := range []BlockID{a, b, c} {
		if !blockSet[want] {
			t.Fatalf("block %d missing from conflict sources", want)
		}
	}
}

func TestRevocationInvalidatesPendingEvidence(t *testing.T) {
	s := NewStore()
	a := mustOpen(t, s, "tenant-a")
	b := mustOpen(t, s, "tenant-b")
	mustAdd(t, s, a, 0, []byte("AAAA"))
	mustAdd(t, s, b, 0, []byte("BBBB"))

	evA := mustAssemble(t, s, Alert{Identity: a, Start: 0, End: 4})
	evB := mustAssemble(t, s, Alert{Identity: b, Start: 0, End: 4})
	if !evA.Valid() || !evB.Valid() {
		t.Fatal("fresh evidence should be valid")
	}
	if err := evA.Verify(); err != nil {
		t.Fatalf("Verify before revoke: %v", err)
	}
	delivered, err := evA.Deliver()
	if err != nil {
		t.Fatalf("Deliver before revoke: %v", err)
	}
	if err := delivered.Verify(); err != nil {
		t.Fatalf("delivered Verify: %v", err)
	}
	if string(delivered.Segments[0].Data) != "AAAA" {
		t.Fatalf("delivered data = %q", delivered.Segments[0].Data)
	}

	if removed := s.Revoke("tenant-a"); removed != 1 {
		t.Fatalf("Revoke removed %d blocks, want 1", removed)
	}
	if evA.Valid() {
		t.Fatal("pending evidence remained valid after tenant revocation")
	}
	if err := evA.Verify(); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Verify after revoke = %v, want ErrRevoked", err)
	}
	if _, err := evA.Trace(0); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Trace after revoke = %v, want ErrRevoked", err)
	}
	if _, err := evA.Deliver(); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Deliver after revoke = %v, want ErrRevoked", err)
	}
	if !evB.Valid() {
		t.Fatal("revoking tenant-a invalidated tenant-b evidence")
	}
	if _, err := s.Add(Fragment{Identity: a, Offset: 0, Data: []byte("late")}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Add after revoke = %v, want ErrRevoked", err)
	}
	if _, err := s.Assemble(Alert{Identity: a, Start: 0, End: 4}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Assemble after revoke = %v, want ErrRevoked", err)
	}
	if _, err := s.OpenConnection("tenant-a"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("OpenConnection after revoke = %v, want ErrRevoked", err)
	}
}

func TestRevokeOneGenerationKeepsOtherGeneration(t *testing.T) {
	s := NewStore()
	g1 := mustOpen(t, s, "tenant-a")
	g2 := mustOpen(t, s, "tenant-a")
	mustAdd(t, s, g1, 0, []byte("old!"))
	mustAdd(t, s, g2, 0, []byte("new!"))

	ev1 := mustAssemble(t, s, Alert{Identity: g1, Start: 0, End: 4})
	ev2 := mustAssemble(t, s, Alert{Identity: g2, Start: 0, End: 4})
	if !ev1.Valid() || !ev2.Valid() {
		t.Fatal("fresh evidence should be valid")
	}
	if removed := s.RevokeIdentity(g1); removed != 1 {
		t.Fatalf("RevokeIdentity removed %d, want 1", removed)
	}
	if ev1.Valid() {
		t.Fatal("revoked generation evidence is still valid")
	}
	if !ev2.Valid() {
		t.Fatal("other generation was invalidated")
	}
	if err := ev2.Verify(); err != nil {
		t.Fatalf("other generation Verify: %v", err)
	}
}

func TestEveryOutputByteHasAnExactIdentityChain(t *testing.T) {
	s := NewStore()
	a := mustOpen(t, s, "tenant-a")
	b := mustOpen(t, s, "tenant-b")

	// Same byte at the same offset would conflict if identities were ignored.
	pattern := func(off uint64) byte { return byte('a' + off%26) }
	data := func(start, n uint64) []byte {
		out := make([]byte, n)
		for i := range out {
			out[i] = pattern(start + uint64(i))
		}
		return out
	}

	// Out-of-order, overlapping, consistent fragments using a tiny reused pool.
	pool := make([]byte, 16)
	for _, span := range []struct{ start, n uint64 }{
		{96, 16}, {32, 16}, {0, 16}, {64, 16}, {16, 16}, {80, 16}, {48, 16},
	} {
		copy(pool, data(span.start, span.n))
		mustAdd(t, s, a, span.start, pool[:span.n])
		// Poison the pool with the other tenant between every Add.
		for i := range pool {
			pool[i] = byte('Z')
		}
		mustAdd(t, s, b, span.start, pool)
	}

	ev := mustAssemble(t, s, Alert{Identity: a, Start: 0, End: 112})
	if err := ev.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	var concatenated []byte
	for _, seg := range ev.Segments {
		if seg.Kind != KindData {
			t.Fatalf("unexpected %s at [%d,%d)", seg.Kind, seg.Start, seg.End)
		}
		concatenated = append(concatenated, seg.Data...)
	}
	if uint64(len(concatenated)) != 112 {
		t.Fatalf("assembled %d bytes, want 112", len(concatenated))
	}
	for off := uint64(0); off < 112; off++ {
		if concatenated[off] != pattern(off) {
			t.Fatalf("byte %d = %q, want %q", off, concatenated[off], pattern(off))
		}
		gotID, err := ev.ByteIdentity(off)
		if err != nil {
			t.Fatalf("ByteIdentity(%d): %v", off, err)
		}
		if !gotID.Equal(a) {
			t.Fatalf("byte %d identity = %s, want %s", off, gotID, a)
		}
		tr, err := ev.Trace(off)
		if err != nil {
			t.Fatalf("Trace(%d): %v", off, err)
		}
		if tr.Kind != KindData || !tr.Identity.Equal(a) {
			t.Fatalf("Trace(%d) = %+v", off, tr)
		}
		covered := false
		for _, src := range tr.Sources {
			if src.Start <= off && off < src.End && src.Identity.Equal(a) {
				covered = true
			}
		}
		if !covered {
			t.Fatalf("byte %d has no exact identity-chain source", off)
		}
	}
}

func TestVerifyDetectsTamperingWithOutputOrProvenance(t *testing.T) {
	s := NewStore()
	id := mustOpen(t, s, "tenant-a")
	mustAdd(t, s, id, 0, []byte("safe"))
	ev := mustAssemble(t, s, Alert{Identity: id, Start: 0, End: 4})

	ev.Segments[0].Data[0] = 'X'
	if err := ev.Verify(); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("tampered segment Verify = %v, want ErrChainBroken", err)
	}

	ev = mustAssemble(t, s, Alert{Identity: id, Start: 0, End: 4})
	ev.Segments[0].Sources[0].Identity.Tenant = "other-tenant"
	if err := ev.Verify(); !errors.Is(err, ErrChainBroken) {
		t.Fatalf("tampered source Verify = %v, want ErrChainBroken", err)
	}
}

func TestInvalidInputs(t *testing.T) {
	s := NewStore()
	if _, err := s.Add(Fragment{Identity: Identity{}, Offset: 0, Data: []byte("x")}); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("empty identity Add = %v", err)
	}
	if _, err := s.Add(Fragment{Identity: Identity{Tenant: "a"}, Offset: ^uint64(0), Data: []byte("xx")}); !errors.Is(err, ErrOffsetOverflow) {
		t.Fatalf("overflowing Add = %v", err)
	}
	if _, err := s.Assemble(Alert{Identity: Identity{Tenant: "a"}, Start: 2, End: 1}); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("wrapped alert = %v", err)
	}
	id := Identity{Tenant: "a"}
	if _, err := s.Assemble(Alert{Identity: id, Start: 7, End: 7}); err != nil {
		t.Fatalf("empty half-open interval: %v", err)
	}
}

func TestZeroValueStoreIsUsable(t *testing.T) {
	var s Store
	id, err := s.OpenConnection("tenant-a")
	if err != nil {
		t.Fatalf("OpenConnection: %v", err)
	}
	mustAdd(t, &s, id, 0, []byte("data"))
	ev := mustAssemble(t, &s, Alert{Identity: id, Start: 0, End: 4})
	if err := ev.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if removed := s.Revoke("tenant-a"); removed != 1 {
		t.Fatalf("Revoke removed %d, want 1", removed)
	}
}

func TestRevocationRacesWithDelivery(t *testing.T) {
	const (
		rows       = 64
		assemblers = 4
	)

	s := NewStore()
	id := mustOpen(t, s, "tenant-race")
	for i := 0; i < rows; i++ {
		start := uint64(i * 4)
		payload := []byte(fmt.Sprintf("%04d", i))
		mustAdd(t, s, id, start, payload)
	}

	var (
		wg            sync.WaitGroup
		startCh       = make(chan struct{})
		firstDelivery = make(chan struct{})
		deliverOnce   sync.Once
		verifyErr     atomic.Value // stores []error
		mu            sync.Mutex
		issued        []*Evidence
		delivered     []*Delivery
	)

	for w := 0; w < assemblers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-startCh
			errs := make([]error, 0)
			for i := 0; i < rows; i++ {
				// Alternate intervals and add more pending blocks while the
				// revoker runs, exercising the delivery/revocation boundary.
				lo := uint64((i + worker) % rows * 4)
				hi := lo + 4
				ev, err := s.Assemble(Alert{Identity: id, Start: lo, End: hi})
				if err != nil {
					if !errors.Is(err, ErrRevoked) {
						errs = append(errs, fmt.Errorf("Assemble: %w", err))
					}
					continue
				}
				mu.Lock()
				issued = append(issued, ev)
				mu.Unlock()
				deliverOnce.Do(func() { close(firstDelivery) })
				if err := ev.Verify(); err != nil && !errors.Is(err, ErrRevoked) {
					errs = append(errs, fmt.Errorf("Verify: %w", err))
				}
				d, err := ev.Deliver()
				if err != nil {
					if !errors.Is(err, ErrRevoked) {
						errs = append(errs, fmt.Errorf("Deliver: %w", err))
					}
					continue
				}
				mu.Lock()
				delivered = append(delivered, d)
				mu.Unlock()
				if err := d.Verify(); err != nil {
					errs = append(errs, fmt.Errorf("delivered Verify: %w", err))
				}
			}
			verifyErr.Store(errs)
		}(w)
	}

	close(startCh)
	// Ensure delivery has begun, then revoke while assemblers are still running.
	<-firstDelivery
	s.Revoke("tenant-race")

	wg.Wait()
	if errs, _ := verifyErr.Load().([]error); len(errs) > 0 {
		t.Fatalf("pre-revocation verification failed: %v", errs[0])
	}

	// Revocation is final: every evidence value that was already handed out,
	// even one assembled just before Revoke returned, must now be invalid.
	mu.Lock()
	defer mu.Unlock()
	if len(issued) == 0 {
		t.Fatal("no evidence was delivered")
	}
	for i, ev := range issued {
		if ev.Valid() {
			t.Fatalf("pending evidence %d remained valid after Revoke returned", i)
		}
		if err := ev.Verify(); !errors.Is(err, ErrRevoked) {
			t.Fatalf("pending evidence %d Verify = %v, want ErrRevoked", i, err)
		}
	}
	for i, d := range delivered {
		if d.Epoch != 0 {
			t.Fatalf("delivery %d observed epoch %d, want pre-revocation epoch 0", i, d.Epoch)
		}
		if err := d.Verify(); err != nil {
			t.Fatalf("already-delivered snapshot %d Verify: %v", i, err)
		}
	}
}

func TestRevocationDuringAssemblyNeverYieldsValidEvidence(t *testing.T) {
	s := NewStore()
	id := mustOpen(t, s, "tenant-race")
	for i := 0; i < 128; i++ {
		mustAdd(t, s, id, uint64(i), []byte{byte(i)})
	}

	var (
		wg       sync.WaitGroup
		startCh  = make(chan struct{})
		valid    atomic.Int64
		badChain atomic.Int64
	)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			for i := 0; i < 256; i++ {
				ev, err := s.Assemble(Alert{Identity: id, Start: 0, End: 128})
				if err != nil {
					continue
				}
				if ev.Valid() {
					valid.Add(1)
					if err := ev.Verify(); err != nil {
						badChain.Add(1)
					}
				}
			}
		}()
	}
	close(startCh)
	s.Revoke("tenant-race")
	wg.Wait()

	// Some evidence may be valid if it was assembled before the revoke took the
	// lock; none may be valid once Revoke has returned.
	for i := 0; i < 256; i++ {
		ev, err := s.Assemble(Alert{Identity: id, Start: 0, End: 128})
		if err != nil {
			continue
		}
		if ev.Valid() {
			t.Fatalf("evidence issued after Revoke returned is valid")
		}
	}
	if badChain.Load() != 0 {
		t.Fatalf("%d evidence values had broken chains", badChain.Load())
	}
	_ = valid.Load()
}
