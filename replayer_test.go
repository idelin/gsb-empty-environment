package main

import "testing"

func TestReplayPostBaseWrapAndEviction(t *testing.T) {
	events := []Event{
		insertEvent(1, "a0"),
		insertEvent(2, "a1"),
		insertEvent(3, "a2"),
		insertEvent(4, "a3"),
		insertEvent(5, "a4"),
		insertEvent(6, "a5"),
		insertEvent(7, "a6"),
		insertEvent(8, "a7"),
		{
			Seq:        9,
			Type:       "header_block",
			StreamID:   4,
			EncodedRIC: 5,
			Sign:       0,
			DeltaBase:  2,
			References: []Reference{{Kind: "post_base", Index: 0}},
		},
		insertEvent(10, "a8"),
		insertEvent(11, "a9"),
		insertEvent(12, "a10"),
		insertEvent(13, "a11"),
		insertEvent(14, "a12"),
		insertEvent(15, "a13"),
		{
			Seq:        16,
			Type:       "header_block",
			StreamID:   8,
			EncodedRIC: 6,
			Sign:       0,
			DeltaBase:  1,
			References: []Reference{{Kind: "indexed", Index: 0}},
		},
		{
			Seq:        17,
			Type:       "header_block",
			StreamID:   12,
			EncodedRIC: 6,
			Sign:       0,
			DeltaBase:  14,
			References: []Reference{{Kind: "indexed", Index: 0}},
		},
		{Seq: 18, Type: "capacity", Capacity: 360},
	}

	result, err := NewReplayer(320, 4).Run(events)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	block4 := findBlock(t, result, 4)
	if block4.Status != "decodable" || block4.FirstDecodableSeq == nil || *block4.FirstDecodableSeq != 14 {
		t.Fatalf("stream 4 = %+v, want decodable at seq 14", block4)
	}
	if block4.RequiredInsertCount != 13 || block4.Base != 11 {
		t.Fatalf("stream 4 RIC/base = %d/%d, want 13/11", block4.RequiredInsertCount, block4.Base)
	}
	assertRefs(t, block4, []int{12})

	block8 := findBlock(t, result, 8)
	if block8.Status != "decodable" || block8.FirstDecodableSeq == nil || *block8.FirstDecodableSeq != 16 {
		t.Fatalf("stream 8 = %+v, want decodable at seq 16", block8)
	}
	if block8.RequiredInsertCount != 14 || block8.Base != 13 {
		t.Fatalf("stream 8 RIC/base = %d/%d, want 14/13", block8.RequiredInsertCount, block8.Base)
	}
	assertRefs(t, block8, []int{13})

	block12 := findBlock(t, result, 12)
	if block12.Status != "evicted" || block12.FirstDecodableSeq != nil {
		t.Fatalf("stream 12 = %+v, want permanently evicted", block12)
	}
	assertRefs(t, block12, []int{0})
	if result.FinalTable.OldestAbsIndex != 6 {
		t.Fatalf("oldest = %d, want 6", result.FinalTable.OldestAbsIndex)
	}
}

func TestReplayBlockedReferenceEvictedByCapacityShrink(t *testing.T) {
	events := []Event{
		insertEvent(1, "a0"),
		insertEvent(2, "a1"),
		insertEvent(3, "a2"),
		{
			Seq:        4,
			Type:       "header_block",
			StreamID:   0,
			EncodedRIC: 7,
			Sign:       0,
			DeltaBase:  6,
			References: []Reference{{Kind: "name", Index: 0}},
		},
		{Seq: 5, Type: "duplicate", AbsIndex: 1},
		{Seq: 6, Type: "capacity", Capacity: 80},
	}

	result, err := NewReplayer(120, 4).Run(events)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	block := findBlock(t, result, 0)
	if block.Status != "evicted" || block.FirstDecodableSeq != nil {
		t.Fatalf("stream 0 = %+v, want evicted", block)
	}
	if block.RequiredInsertCount != 7 || block.Base != 1 {
		t.Fatalf("RIC/base = %d/%d, want 7/1", block.RequiredInsertCount, block.Base)
	}
	assertRefs(t, block, []int{1})
	if result.FinalTable.OldestAbsIndex != 2 || result.FinalTable.InsertCount != 4 {
		t.Fatalf("table = %+v, want oldest 2 and insert count 4", result.FinalTable)
	}
}

func TestReplayRejectsInvalidReferences(t *testing.T) {
	events := []Event{
		insertEvent(1, "a0"),
		{
			Seq:        2,
			Type:       "header_block",
			StreamID:   0,
			EncodedRIC: 1,
			Sign:       0,
			DeltaBase:  0,
			References: []Reference{{Kind: "indexed", Index: 2}},
		},
		{
			Seq:        3,
			Type:       "header_block",
			StreamID:   1,
			EncodedRIC: 1,
			Sign:       0,
			DeltaBase:  0,
			References: []Reference{{Kind: "post_base", Index: 0}},
		},
	}

	result, err := NewReplayer(120, 4).Run(events)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if block := findBlock(t, result, 0); block.Status != "invalid" {
		t.Fatalf("stream 0 = %+v, want invalid", block)
	}
	if block := findBlock(t, result, 1); block.Status != "invalid" {
		t.Fatalf("stream 1 = %+v, want invalid", block)
	}
}

func TestDecodeRequiredInsertCount(t *testing.T) {
	tests := []struct {
		encoded    int
		fullRange  int
		maxEntries int
		want       int
	}{
		{0, 0, 4, 0},
		{1, 1, 4, 1},
		{1, 8, 4, 9},
		{2, 10, 4, 10},
		{3, 11, 4, 11},
		{8, 12, 4, 16},
		{3, 13, 4, 11},
	}

	for _, tt := range tests {
		got, err := decodeRequiredInsertCount(tt.encoded, tt.fullRange, tt.maxEntries)
		if err != nil {
			t.Fatalf("decodeRequiredInsertCount(%d, %d) error = %v", tt.encoded, tt.fullRange, err)
		}
		if got != tt.want {
			t.Fatalf("decodeRequiredInsertCount(%d, %d) = %d, want %d", tt.encoded, tt.fullRange, got, tt.want)
		}
	}
}

func TestDecodeRequiredInsertCountRejectsOutOfRangeEncoding(t *testing.T) {
	if _, err := decodeRequiredInsertCount(256, 300, 256); err == nil {
		t.Fatal("encoded RIC 256 was accepted")
	}
	if _, err := decodeRequiredInsertCount(9, 12, 4); err == nil {
		t.Fatal("encoded RIC above 2*max_entries was accepted")
	}
	if _, err := decodeRequiredInsertCount(1, 1, 0); err == nil {
		t.Fatal("non-zero RIC with zero maximum entries was accepted")
	}
	if _, err := decodeRequiredInsertCount(0, 1, 4); err == nil {
		t.Fatal("encoded zero RIC was accepted after a dynamic insertion")
	}
}

func insertEvent(seq int, name string) Event {
	return Event{Seq: seq, Type: "insert", Name: name, Value: "v", Size: 40}
}

func findBlock(t *testing.T, result ReplayResult, streamID int) BlockResult {
	t.Helper()
	for _, block := range result.Blocks {
		if block.StreamID == streamID {
			return block
		}
	}
	t.Fatalf("stream %d not in results: %+v", streamID, result.Blocks)
	return BlockResult{}
}

func assertRefs(t *testing.T, block BlockResult, want []int) {
	t.Helper()
	if len(block.AbsoluteReferences) != len(want) {
		t.Fatalf("refs = %v, want %v", block.AbsoluteReferences, want)
	}
	for index := range want {
		if block.AbsoluteReferences[index] != want[index] {
			t.Fatalf("refs = %v, want %v", block.AbsoluteReferences, want)
		}
	}
}
