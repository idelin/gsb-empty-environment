package fmp4mux

import (
	"encoding/binary"
	"testing"
)

// TestBoxStructureAndTrunLayout verifies the exact box hierarchy and the
// trun data_offset/sample entry layout at the byte level.
func TestBoxStructureAndTrunLayout(t *testing.T) {
	cfg := Config{
		Timescale: testTimescale,
		SPS:       []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:       []byte{0x68, 0xce},
	}
	f := NewFragmenter(cfg)

	// Fragment 1: IDR + P, then a second IDR forces a flush.
	_, _, err := f.PushSample(Sample{DTS: 0, CTO: 6000, Key: true,
		AU: annexB(nalBytes(nalTypeIDR, 1))})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.PushSample(Sample{DTS: 3000, CTO: -3000,
		AU: annexB(nalBytes(1, 2))})
	if err != nil {
		t.Fatal(err)
	}
	seg, sidx, err := f.PushSample(Sample{DTS: 6000, CTO: 0, Key: true,
		AU: annexB(nalBytes(nalTypeIDR, 3))})
	if err != nil {
		t.Fatal(err)
	}
	if seg == nil {
		t.Fatal("expected flushed segment on second IDR")
	}

	// sidx is emitted separately; parse the bare segment (styp/moof/mdat).
	boxes, err := ParseBoxes(seg)
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 3 ||
		boxes[0].Type != "styp" || boxes[1].Type != "moof" || boxes[2].Type != "mdat" {
		t.Fatalf("segment boxes = %v", boxTypes(boxes))
	}

	moof := boxes[1]
	if len(moof.Children) != 2 || moof.Children[0].Type != "mfhd" ||
		moof.Children[1].Type != "traf" {
		t.Fatalf("moof children = %v", boxTypes(moof.Children))
	}
	traf := moof.Children[1]
	wantTraf := []string{"tfhd", "tfdt", "trun"}
	if len(traf.Children) != 3 {
		t.Fatalf("traf children = %v", boxTypes(traf.Children))
	}
	for i, want := range wantTraf {
		if traf.Children[i].Type != want {
			t.Fatalf("traf child %d = %s, want %s", i, traf.Children[i].Type, want)
		}
	}

	mfhd := moof.Children[0]
	tfhd := traf.Children[0]
	tfdt := traf.Children[1]
	trun := traf.Children[2]

	if binary.BigEndian.Uint32(mfhd.Payload) != 1 {
		t.Error("mfhd sequence_number must start at 1")
	}
	if tfhd.Flags != 0x020000 {
		t.Errorf("tfhd flags = %#x, want 0x020000 (default-base-is-moof)", tfhd.Flags)
	}
	if tfdt.Version != 1 {
		t.Error("tfdt must be version 1 (64-bit baseMediaDecodeTime)")
	}
	if binary.BigEndian.Uint64(tfdt.Payload) != 0 {
		t.Error("first fragment tfdt baseMediaDecodeTime must be 0")
	}
	if trun.Version != 1 {
		t.Error("trun must be version 1 with signed composition offsets")
	}

	// trun flags must enable data-offset, duration, size, flags and CTO.
	wantFlags := uint32(0x000f01)
	if trun.Flags != wantFlags {
		t.Fatalf("trun flags = %#x, want %#x", trun.Flags, wantFlags)
	}

	// data_offset points at mdat payload: moof size + 8-byte mdat header.
	dataOffset := int64(int32(binary.BigEndian.Uint32(trun.Payload[4:8])))
	if dataOffset != int64(moof.Size)+8 {
		t.Errorf("data_offset = %d, want %d", dataOffset, moof.Size+8)
	}

	// First sample entry: duration 3000, correct size, sync flag, CTO +6000.
	count := binary.BigEndian.Uint32(trun.Payload[:4])
	if count != 2 {
		t.Fatalf("trun sample_count = %d, want 2", count)
	}
	entry0 := trun.Payload[8:] // after sample_count + data_offset
	if got := binary.BigEndian.Uint32(entry0[0:4]); got != 3000 {
		t.Errorf("sample0 duration = %d, want 3000", got)
	}
	wantSize := uint32(len(toAVCSample(annexB(nalBytes(nalTypeIDR, 1)))))
	if got := binary.BigEndian.Uint32(entry0[4:8]); got != wantSize {
		t.Errorf("sample0 size = %d, want %d", got, wantSize)
	}
	if got := binary.BigEndian.Uint32(entry0[8:12]); got != flagSyncSample {
		t.Errorf("sample0 flags = %#x, want %#x", got, flagSyncSample)
	}
	if got := int32(binary.BigEndian.Uint32(entry0[12:16])); got != 6000 {
		t.Errorf("sample0 signed CTO = %d, want 6000", got)
	}

	// Second sample: non-sync flag and negative CTO stored as signed int32.
	entry1 := entry0[16:]
	if got := binary.BigEndian.Uint32(entry1[8:12]); got != flagNonSyncSample {
		t.Errorf("sample1 flags = %#x, want %#x", got, flagNonSyncSample)
	}
	if got := int32(binary.BigEndian.Uint32(entry1[12:16])); got != -3000 {
		t.Errorf("sample1 signed CTO = %d, want -3000", got)
	}

	// The separately emitted sidx references exactly styp+moof+mdat bytes.
	sidxBoxes, err := ParseBoxes(sidx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sidxBoxes) != 1 || sidxBoxes[0].Type != "sidx" {
		t.Fatalf("sidx parse: %v", boxTypes(sidxBoxes))
	}

	// Close the leftover fragment and ensure sequence number increments.
	lastSeg, _, err := f.Close()
	if err != nil || lastSeg == nil {
		t.Fatalf("Close: seg=%v err=%v", lastSeg, err)
	}
	lastBoxes, err := ParseBoxes(lastSeg)
	if err != nil {
		t.Fatal(err)
	}
	lastMfhd := FindBox(lastBoxes, "mfhd")
	if binary.BigEndian.Uint32(lastMfhd.Payload) != 2 {
		t.Error("second fragment sequence_number must be 2")
	}
}
