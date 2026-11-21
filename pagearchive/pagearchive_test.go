package pagearchive

import (
	"bytes"
	"errors"
	"testing"
)

func TestCanonicalPatchAndULEB128Boundary(t *testing.T) {
	base := make([]byte, 300)
	target := make([]byte, 300)
	target[0] = 1
	target[127] = 2
	target[128] = 3
	target[299] = 4

	patch, err := EncodePatch(base, target)
	if err != nil {
		t.Fatal(err)
	}

	want := []byte{0, 1, 1}
	want = appendULEB128(want, 126)
	want = appendULEB128(want, 2)
	want = append(want, 2, 3)
	want = appendULEB128(want, 170)
	want = appendULEB128(want, 1)
	want = append(want, 4)
	if !bytes.Equal(patch, want) {
		t.Fatalf("canonical patch = %v, want %v", patch, want)
	}

	decoded, err := DecodePatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, target) {
		t.Fatalf("decoded patch = %v, want %v", decoded, target)
	}

	if _, _, err := readULEB128([]byte{0x80, 0x00}); err == nil {
		t.Fatal("expected non-minimal LEB128 to be rejected")
	}
	maxULEB128 := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	value, n, err := readULEB128(maxULEB128)
	if err != nil || value != ^uint64(0) || n != len(maxULEB128) {
		t.Fatalf("max LEB128 = value %d, n %d, err %v", value, n, err)
	}
	overflowing := append([]byte(nil), maxULEB128[:9]...)
	overflowing = append(overflowing, 0x02)
	if _, _, err := readULEB128(overflowing); err == nil {
		t.Fatal("expected overflowing LEB128 to be rejected")
	}

	simpleBase := []byte{1, 2}
	if _, err := DecodePatch(simpleBase, []byte{0, 1, 9}); err != nil {
		t.Fatalf("valid one-byte patch rejected: %v", err)
	}
	if _, err := DecodePatch(simpleBase, []byte{0, 1, 9, 0, 1, 2}); err == nil {
		t.Fatal("expected non-canonical unchanged byte to be rejected")
	}
}

func TestEmptyPatchAndDecoder(t *testing.T) {
	page := bytes.Repeat([]byte{7}, 8)
	patch, err := EncodePatch(page, append([]byte(nil), page...))
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 0 {
		t.Fatalf("empty patch length = %d, want 0", len(patch))
	}

	layout := &Layout{Representations: []Representation{
		{Page: 0, Full: true, Payload: append([]byte(nil), page...), MetadataCost: 2},
		{Page: 1, Base: 0, Payload: patch, MetadataCost: 2},
	}}
	reader, err := NewReader(layout)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.Read(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, page) {
		t.Fatalf("decoded zero-difference page = %v, want %v", got, page)
	}

	archive, err := Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Unmarshal(archive, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	if restored.PersistentBytes != 12 {
		t.Fatalf("persistent bytes = %d, want 12", restored.PersistentBytes)
	}
	if _, err := NewReader(restored); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveRejectsTrailingAndMalformedData(t *testing.T) {
	layout := &Layout{Representations: []Representation{
		{Page: 0, Full: true, Payload: []byte{1, 2}, MetadataCost: 0},
	}}
	archive, err := Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Unmarshal(append(archive, 0), 2, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("trailing data error = %v, want ErrInvalidInput", err)
	}

	bad := append([]byte(nil), archive...)
	bad[archiveHeaderSize] = 7
	if _, err := Unmarshal(bad, 2, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad flags error = %v, want ErrInvalidInput", err)
	}

	layout.Representations[0].Payload = []byte{1}
	badArchive, err := Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Unmarshal(badArchive, 2, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad full-page length error = %v, want ErrInvalidInput", err)
	}
}

func TestPlanEnumeratesLayoutsAndOptimizesReadThenStorage(t *testing.T) {
	pages := [][]byte{
		{1, 1, 1, 1},
		{2, 1, 1, 1},
		{3, 1, 1, 1},
	}
	input := PlanInput{
		Pages:          pages,
		Snapshots:      [][]int{{0, 1, 2}},
		CandidateEdges: []CandidateEdge{{From: 0, To: 1}, {From: 0, To: 2}},
		MetadataCost:   1,
		Budget:         100,
		MaxDepth:       MaxDependencyDepth,
		CacheCapacity:  2,
	}

	layout, err := Plan(input)
	if err != nil {
		t.Fatal(err)
	}

	wantRead := 0
	for _, representation := range layout.Representations {
		wantRead += representation.PersistentCost()
	}
	if layout.MaxSnapshotReadBytes != wantRead {
		t.Fatalf("max read = %d, want exact miss total %d", layout.MaxSnapshotReadBytes, wantRead)
	}

	reader, err := NewReader(layout)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := reader.NewSnapshotCache(2)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range []int{0, 1, 2} {
		got, err := cache.Read(page)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pages[page]) {
			t.Fatalf("page %d = %v, want %v", page, got, pages[page])
		}
	}
	if cache.ReadBytes() != layout.MaxSnapshotReadBytes {
		t.Fatalf("decoder read %d bytes, planner expected %d", cache.ReadBytes(), layout.MaxSnapshotReadBytes)
	}

	minimumStorage := (4 + 1) + 2*(4+1)
	for _, patchCost := range []int{len(canonicalPatch(pages[0], pages[1])), len(canonicalPatch(pages[0], pages[2]))} {
		minimumStorage -= 4 + 1
		minimumStorage += patchCost + 1
	}
	if layout.PersistentBytes != minimumStorage {
		t.Fatalf("storage = %d, want minimum %d", layout.PersistentBytes, minimumStorage)
	}
}

func TestSharedBaseCausesNonLocalEvictionAndIndependentSnapshots(t *testing.T) {
	page0 := []byte{1, 1, 1, 1}
	page1 := []byte{2, 1, 1, 1}
	page2 := []byte{3, 1, 1, 1}
	page3 := []byte{4, 1, 1, 1}
	pages := [][]byte{page0, page1, page2, page3}
	layout := &Layout{Representations: []Representation{
		{Page: 0, Full: true, Payload: page0, MetadataCost: 1},
		{Page: 1, Base: 0, Payload: canonicalPatch(page0, page1), MetadataCost: 1},
		{Page: 2, Base: 0, Payload: canonicalPatch(page0, page2), MetadataCost: 1},
		{Page: 3, Base: 0, Payload: canonicalPatch(page0, page3), MetadataCost: 1},
	}}
	reader, err := NewReader(layout)
	if err != nil {
		t.Fatal(err)
	}

	cache, err := reader.NewSnapshotCache(3)
	if err != nil {
		t.Fatal(err)
	}
	accesses := []int{0, 1, 2, 3, 1}
	wantReads := []int{5, 4, 4, 4, 4}
	total := 0
	for i, page := range accesses {
		got, err := cache.Read(page)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pages[page]) {
			t.Fatalf("page %d = %v, want %v", page, got, pages[page])
		}
		total += wantReads[i]
		if cache.ReadBytes() != total {
			t.Fatalf("after access %d read bytes = %d, want %d", i, cache.ReadBytes(), total)
		}
	}
	if cache.Len() != 3 {
		t.Fatalf("cache length = %d, want 3", cache.Len())
	}

	other, err := reader.NewSnapshotCache(3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Read(0); err != nil {
		t.Fatal(err)
	}
	if other.ReadBytes() != 5 {
		t.Fatalf("independent snapshot read = %d, want 5", other.ReadBytes())
	}
}

func TestRejectsPatchCycleAndDepthLimit(t *testing.T) {
	page := []byte{1, 2, 3, 4}
	zeroPatch := []byte{}
	layout := &Layout{Representations: []Representation{
		{Page: 0, Base: 1, Payload: zeroPatch, MetadataCost: 1},
		{Page: 1, Base: 0, Payload: zeroPatch, MetadataCost: 1},
	}}
	if _, err := NewReader(layout); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cycle error = %v, want ErrInvalidInput", err)
	}

	pages := [][]byte{page, {5, 2, 3, 4}, {5, 6, 3, 4}, {5, 6, 7, 4}, {5, 6, 7, 8}}
	edges := []CandidateEdge{{0, 1}, {1, 2}, {2, 3}, {3, 4}}
	_, err := Plan(PlanInput{
		Pages:          pages,
		CandidateEdges: edges,
		MetadataCost:   0,
		Budget:         4,
		MaxDepth:       3,
		CacheCapacity:  3,
	})
	if !errors.Is(err, ErrNoFeasibleLayout) {
		t.Fatalf("depth-limit error = %v, want ErrNoFeasibleLayout", err)
	}
}

func TestBudgetOneByteTooSmallAndExactBudget(t *testing.T) {
	pages := [][]byte{{1, 2, 3}, {4, 2, 3}}
	input := PlanInput{
		Pages:          pages,
		CandidateEdges: []CandidateEdge{{0, 1}},
		MetadataCost:   1,
		Budget:         0,
		MaxDepth:       MaxDependencyDepth,
		CacheCapacity:  3,
	}

	minimum := 0
	for _, page := range pages {
		minimum += len(page) + 1
	}
	input.Budget = minimum - 1
	if _, err := Plan(input); !errors.Is(err, ErrNoFeasibleLayout) {
		t.Fatalf("short-budget error = %v, want ErrNoFeasibleLayout", err)
	}

	input.Budget = minimum
	layout, err := Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	if layout.PersistentBytes > minimum {
		t.Fatalf("storage = %d, cannot exceed exact budget %d", layout.PersistentBytes, minimum)
	}
}

func TestSnapshotResultsMatchIndependentCaches(t *testing.T) {
	pages := [][]byte{
		{1, 1, 1},
		{2, 1, 1},
		{3, 1, 1},
	}
	input := PlanInput{
		Pages:          pages,
		Snapshots:      [][]int{{0, 1, 0}, {1, 2, 1}},
		CandidateEdges: []CandidateEdge{{0, 1}, {1, 2}},
		MetadataCost:   1,
		Budget:         100,
		MaxDepth:       MaxDependencyDepth,
		CacheCapacity:  1,
	}
	layout, err := Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewReader(layout)
	if err != nil {
		t.Fatal(err)
	}

	for snapshot, accesses := range input.Snapshots {
		cache, err := reader.NewSnapshotCache(1)
		if err != nil {
			t.Fatal(err)
		}
		for _, page := range accesses {
			if _, err := cache.Read(page); err != nil {
				t.Fatal(err)
			}
		}
		if cache.ReadBytes() != layout.SnapshotReadBytes[snapshot] {
			t.Fatalf("snapshot %d read %d, planner computed %d", snapshot, cache.ReadBytes(), layout.SnapshotReadBytes[snapshot])
		}
	}

	expectedMax := 0
	for _, read := range layout.SnapshotReadBytes {
		if read > expectedMax {
			expectedMax = read
		}
	}
	if layout.MaxSnapshotReadBytes != expectedMax {
		t.Fatalf("max = %d, want %d", layout.MaxSnapshotReadBytes, expectedMax)
	}
}

func buildChainLayout(t *testing.T, pages [][]byte, metadata int) *Layout {
	t.Helper()
	representations := make([]Representation, len(pages))
	representations[0] = Representation{Page: 0, Full: true, Payload: append([]byte(nil), pages[0]...), MetadataCost: metadata}
	for page := 1; page < len(pages); page++ {
		patch, err := EncodePatch(pages[page-1], pages[page])
		if err != nil {
			t.Fatal(err)
		}
		representations[page] = Representation{Page: page, Base: page - 1, Payload: patch, MetadataCost: metadata}
	}
	return &Layout{Representations: representations}
}

func TestRepresentationLayoutShape(t *testing.T) {
	pages := [][]byte{{1, 2, 3, 4}, {9, 2, 3, 4}}
	layout, err := Plan(PlanInput{
		Pages:          pages,
		CandidateEdges: []CandidateEdge{{0, 1}},
		MetadataCost:   0,
		Budget:         7,
		MaxDepth:       MaxDependencyDepth,
		CacheCapacity:  3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !layout.Representations[0].Full || layout.Representations[1].Full || layout.Representations[1].Base != 0 {
		t.Fatalf("unexpected representations: %#v", layout.Representations)
	}
	if layout.DependencyDepth != 1 {
		t.Fatalf("depth = %d, want 1", layout.DependencyDepth)
	}
}

func TestPlanAllowsBackwardCandidateEdgeAndRejectsShortestCycle(t *testing.T) {
	pages := [][]byte{{1, 2, 3, 4}, {9, 2, 3, 4}}
	layout, err := Plan(PlanInput{
		Pages:          pages,
		CandidateEdges: []CandidateEdge{{From: 1, To: 0}},
		MetadataCost:   0,
		Budget:         7,
		MaxDepth:       MaxDependencyDepth,
		CacheCapacity:  3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if layout.Representations[0].Full || layout.Representations[0].Base != 1 || !layout.Representations[1].Full {
		t.Fatalf("backward edge not selected: %#v", layout.Representations)
	}

	_, err = Plan(PlanInput{
		Pages:          pages,
		CandidateEdges: []CandidateEdge{{From: 0, To: 1}, {From: 1, To: 0}},
		MetadataCost:   0,
		Budget:         7,
		MaxDepth:       MaxDependencyDepth,
		CacheCapacity:  3,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestForwardEdgesHonorDepthAndRejectCycle(t *testing.T) {
	pages := make([][]byte, 5)
	for i := range pages {
		pages[i] = make([]byte, 4)
		pages[i][0] = byte(i + 1)
	}
	patchLength := len(canonicalPatch(pages[0], pages[1]))
	edges := []CandidateEdge{{1, 0}, {2, 1}, {3, 2}, {4, 3}}
	input := PlanInput{
		Pages:          pages,
		CandidateEdges: edges,
		MetadataCost:   0,
		Budget:         4 + 4*patchLength,
		MaxDepth:       3,
		CacheCapacity:  3,
	}
	if _, err := Plan(input); !errors.Is(err, ErrNoFeasibleLayout) {
		t.Fatalf("forward depth error = %v, want ErrNoFeasibleLayout", err)
	}

	input.MaxDepth = 4
	layout, err := Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	if layout.DependencyDepth != 4 {
		t.Fatalf("forward dependency depth = %d, want 4", layout.DependencyDepth)
	}

	cycleLayout := &Layout{Representations: []Representation{
		{Page: 0, Base: 2, Payload: []byte{}, MetadataCost: 0},
		{Page: 1, Base: 0, Payload: []byte{}, MetadataCost: 0},
		{Page: 2, Base: 1, Payload: []byte{}, MetadataCost: 0},
	}}
	if _, err := NewReader(cycleLayout); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("3-page cycle error = %v, want ErrInvalidInput", err)
	}
}

func TestEmptySnapshotsOptimizeStorageAndCandidateCycle(t *testing.T) {
	pages := [][]byte{{1, 2, 3, 4}, {9, 2, 3, 4}, {8, 2, 3, 4}}
	patch01 := canonicalPatch(pages[0], pages[1])
	patch12 := canonicalPatch(pages[1], pages[2])
	patch20 := canonicalPatch(pages[2], pages[0])
	layout, err := Plan(PlanInput{
		Pages:          pages,
		Snapshots:      nil,
		CandidateEdges: []CandidateEdge{{From: 0, To: 1}, {From: 1, To: 2}, {From: 2, To: 0}},
		MetadataCost:   0,
		Budget:         4 + len(patch01) + len(patch12),
		MaxDepth:       MaxDependencyDepth,
		CacheCapacity:  3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if layout.MaxSnapshotReadBytes != 0 || len(layout.SnapshotReadBytes) != 0 {
		t.Fatalf("empty workload result = read %d, reads %v", layout.MaxSnapshotReadBytes, layout.SnapshotReadBytes)
	}
	minimumStorage := 4 + len(patch20) + len(patch01)
	if layout.PersistentBytes != minimumStorage {
		t.Fatalf("storage = %d, want %d", layout.PersistentBytes, minimumStorage)
	}

	reader, err := NewReader(layout)
	if err != nil {
		t.Fatal(err)
	}
	for page, want := range pages {
		got, err := reader.Read(page)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("page %d = %v, want %v", page, got, want)
		}
	}
}
