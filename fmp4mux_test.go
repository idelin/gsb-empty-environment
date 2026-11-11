package fmp4mux

import (
	"encoding/binary"
	"testing"
)

const (
	testTimescale = 90000
	testFrameDur  = 3000 // 30 fps
	gopLen        = 4    // decode-order GOP: I P B B
)

func nalBytes(nalType uint8, id int) []byte {
	payload := make([]byte, 8+id%16)
	payload[0] = 0x80 | nalType
	for i := 1; i < len(payload); i++ {
		payload[i] = byte((id*7 + i) & 0xff)
	}
	return payload
}

func annexB(nals ...[]byte) []byte {
	var out []byte
	for _, n := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	return out
}

func lengthPrefixed(nals ...[]byte) []byte {
	var out []byte
	for _, n := range nals {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(n)))
		out = append(out, l[:]...)
		out = append(out, n...)
	}
	return out
}

// gopSamples builds one 4-frame GOP in decode order (I, P, B, B). The global
// delay shift makes the earliest presentation time non-negative while still
// exercising negative composition offsets for the B frames.
func gopSamples(gopIndex, dtsBase uint64, delay int64) []Sample {
	idBase := int(gopIndex) * 100
	return []Sample{
		{
			DTS: dtsBase + 0,
			CTO: int32(delay + 6000),
			Key: true,
			AU: annexB(
				nalBytes(nalTypeSPS, idBase+1),
				nalBytes(nalTypePPS, idBase+2),
				nalBytes(nalTypeIDR, idBase+3),
			),
		},
		{
			DTS: dtsBase + 3000,
			CTO: int32(delay - 3000),
			AU:  annexB(nalBytes(1, idBase+4)),
		},
		{
			DTS: dtsBase + 6000,
			CTO: int32(delay - 3000),
			AU:  annexB(nalBytes(1, idBase+5)),
		},
		{
			DTS: dtsBase + 9000,
			CTO: int32(delay),
			AU:  annexB(nalBytes(1, idBase+6)),
		},
	}
}

func boxTypes(boxes []*Box) []string {
	out := make([]string, len(boxes))
	for i, b := range boxes {
		out[i] = b.Type
	}
	return out
}

// sampleDuration reconstructs sample i's duration from the trun payload.
func sampleDuration(seg *MediaSegment, i int) uint32 {
	trun := FindBox(seg.Boxes, "trun")
	p := trun.Payload[4:] // sample_count
	if trun.Flags&0x000001 != 0 {
		p = p[4:] // data_offset
	}
	if trun.Flags&0x01000000 != 0 {
		p = p[4:] // first_sample_flags (trun flag 0x01000000)
	}
	entrySize := 4 // duration
	if trun.Flags&0x000200 != 0 {
		entrySize += 4
	}
	if trun.Flags&0x000400 != 0 {
		entrySize += 4
	}
	if trun.Flags&0x000800 != 0 {
		entrySize += 4
	}
	return binary.BigEndian.Uint32(p[i*entrySize:])
}

func TestRoundTripBFrames(t *testing.T) {
	cfg := Config{Timescale: testTimescale, Width: 1280, Height: 720}
	f := NewFragmenter(cfg)

	const gopCount = 3
	const delay = int64(6000)
	var inputs []Sample
	type emitted struct {
		segment []byte
		sidx    []byte
	}
	var segments []emitted

	for g := 0; g < gopCount; g++ {
		base := uint64(g*gopLen) * testFrameDur
		for _, s := range gopSamples(uint64(g), base, delay) {
			inputs = append(inputs, s)
			seg, sidx, err := f.PushSample(s)
			if err != nil {
				t.Fatalf("PushSample: %v", err)
			}
			if seg != nil {
				segments = append(segments, emitted{seg, sidx})
			}
		}
	}

	lastSeg, lastSIDX, err := f.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if lastSeg != nil {
		segments = append(segments, emitted{lastSeg, lastSIDX})
	}
	if len(segments) != gopCount {
		t.Fatalf("segment count = %d, want %d", len(segments), gopCount)
	}

	initSeg := f.InitSegment()
	if initSeg == nil {
		t.Fatal("missing initialization segment")
	}
	verifyInitSegment(t, initSeg, cfg)

	var allSamples []SampleInfo
	for i, e := range segments {
		combined := append(append([]byte{}, e.sidx...), e.segment...)
		seg, err := ParseMediaSegment(combined)
		if err != nil {
			t.Fatalf("segment %d parse: %v", i, err)
		}
		if seg.SequenceNumber != uint32(i+1) {
			t.Fatalf("segment %d sequence = %d", i, seg.SequenceNumber)
		}
		if len(seg.Samples) != gopLen {
			t.Fatalf("segment %d sample count = %d", i, len(seg.Samples))
		}
		verifySegment(t, seg, i)
		allSamples = append(allSamples, seg.Samples...)
	}

	if len(allSamples) != len(inputs) {
		t.Fatalf("total samples = %d, want %d", len(allSamples), len(inputs))
	}

	// Decode order (DTS) and display time (PTS) must match the input.
	for i, in := range inputs {
		got := allSamples[i]
		if got.DTS != in.DTS {
			t.Errorf("sample %d DTS = %d, want %d", i, got.DTS, in.DTS)
		}
		if got.CTO != in.CTO {
			t.Errorf("sample %d CTO = %d, want %d", i, got.CTO, in.CTO)
		}
		wantPTS := int64(in.DTS) + int64(in.CTO)
		if got.PTS != wantPTS {
			t.Errorf("sample %d PTS = %d, want %d", i, got.PTS, wantPTS)
		}
		if got.SyncSample != in.Key {
			t.Errorf("sample %d sync = %v, want %v", i, got.SyncSample, in.Key)
		}
		wantData := toAVCSample(in.AU)
		if string(got.Data) != string(wantData) {
			t.Errorf("sample %d payload mismatch (%d vs %d bytes)",
				i, len(got.Data), len(wantData))
		}
		if got.Size != uint32(len(wantData)) {
			t.Errorf("sample %d size = %d, want %d", i, got.Size, len(wantData))
		}
	}

	// Display order: PTS-sorted samples within each GOP match display order.
	for g := 0; g < gopCount; g++ {
		base := int64(g*gopLen)*testFrameDur + delay
		wantDisplayPTS := []int64{base, base + 3000, base + 6000, base + 9000}
		displayOrder := make([]SampleInfo, gopLen)
		copy(displayOrder, allSamples[g*gopLen:(g+1)*gopLen])
		for i := 0; i < len(displayOrder); i++ {
			for j := i + 1; j < len(displayOrder); j++ {
				if displayOrder[j].PTS < displayOrder[i].PTS {
					displayOrder[i], displayOrder[j] = displayOrder[j], displayOrder[i]
				}
			}
		}
		for i, want := range wantDisplayPTS {
			if displayOrder[i].PTS != want {
				t.Errorf("gop %d display slot %d PTS = %d, want %d",
					g, i, displayOrder[i].PTS, want)
			}
		}
	}
}

func verifyInitSegment(t *testing.T, data []byte, cfg Config) {
	t.Helper()
	boxes, err := ParseBoxes(data)
	if err != nil {
		t.Fatalf("parse init: %v", err)
	}
	if len(boxes) != 2 || boxes[0].Type != "ftyp" || boxes[1].Type != "moov" {
		t.Fatalf("init top-level boxes = %v", boxTypes(boxes))
	}
	for _, typ := range []string{"mvhd", "trak", "tkhd", "mdia", "mdhd", "hdlr",
		"minf", "vmhd", "dinf", "dref", "stbl", "stsd", "avc1", "avcC",
		"stts", "stsc", "stsz", "stco", "mvex", "trex"} {
		if FindBox(boxes, typ) == nil {
			t.Errorf("init segment missing box %s", typ)
		}
	}
	info, err := ParseInit(data)
	if err != nil {
		t.Fatalf("ParseInit: %v", err)
	}
	if info.Timescale != cfg.Timescale {
		t.Errorf("timescale = %d, want %d", info.Timescale, cfg.Timescale)
	}
	if info.Width != cfg.Width || info.Height != cfg.Height {
		t.Errorf("dimensions = %dx%d, want %dx%d",
			info.Width, info.Height, cfg.Width, cfg.Height)
	}
	if len(info.AVCC) == 0 {
		t.Error("avcC config record missing")
	}
}

func verifySegment(t *testing.T, seg *MediaSegment, gopIndex int) {
	t.Helper()
	wantTop := []string{"sidx", "styp", "moof", "mdat"}
	if boxTypes(seg.Boxes)[0] != "sidx" || len(seg.Boxes) != len(wantTop) {
		t.Fatalf("segment %d boxes = %v, want %v",
			gopIndex, boxTypes(seg.Boxes), wantTop)
	}
	for i, want := range wantTop {
		if seg.Boxes[i].Type != want {
			t.Fatalf("segment %d box %d = %s, want %s",
				gopIndex, i, seg.Boxes[i].Type, want)
		}
	}

	// Negative CTO present -> signed (version 1) trun required.
	if seg.TrunVersion != 1 {
		t.Errorf("segment %d trun version = %d, want 1", gopIndex, seg.TrunVersion)
	}

	wantBase := uint64(gopIndex*gopLen) * testFrameDur
	if seg.BaseMediaDecodeTime != wantBase {
		t.Errorf("segment %d tfdt base = %d, want %d",
			gopIndex, seg.BaseMediaDecodeTime, wantBase)
	}

	for i := range seg.Samples {
		wantDur := uint32(testFrameDur)
		gotDur := sampleDuration(seg, i)
		if gotDur != wantDur {
			t.Errorf("segment %d sample %d duration = %d, want %d",
				gopIndex, i, gotDur, wantDur)
		}
	}

	for i, s := range seg.Samples {
		if s.SyncSample != (i == 0) {
			t.Errorf("segment %d sample %d sync = %v, want %v",
				gopIndex, i, s.SyncSample, i == 0)
		}
	}

	if seg.SIDX == nil {
		t.Fatalf("segment %d missing sidx", gopIndex)
	}
	sidx := seg.SIDX
	if sidx.Version != 1 || sidx.Timescale != testTimescale {
		t.Errorf("segment %d sidx version/timescale = %d/%d",
			gopIndex, sidx.Version, sidx.Timescale)
	}
	if sidx.SAPType != 1 {
		t.Errorf("segment %d SAP type = %d, want 1", gopIndex, sidx.SAPType)
	}
	if sidx.FirstOffset != 0 {
		t.Errorf("segment %d first_offset = %d, want 0",
			gopIndex, sidx.FirstOffset)
	}

	var minPTS int64 = seg.Samples[0].PTS
	var totalDur uint64
	for _, s := range seg.Samples {
		if s.PTS < minPTS {
			minPTS = s.PTS
		}
	}
	for i := range seg.Samples {
		totalDur += uint64(sampleDuration(seg, i))
	}
	if sidx.EarliestPTS != minPTS {
		t.Errorf("segment %d sidx EPT = %d, want %d",
			gopIndex, sidx.EarliestPTS, minPTS)
	}
	if sidx.SubsegmentDuration != totalDur {
		t.Errorf("segment %d sidx duration = %d, want %d",
			gopIndex, sidx.SubsegmentDuration, totalDur)
	}

	var referenced uint32
	for _, b := range seg.Boxes {
		if b.Type == "styp" || b.Type == "moof" || b.Type == "mdat" {
			referenced += uint32(b.Size)
		}
	}
	if sidx.ReferencedSize != referenced {
		t.Errorf("segment %d sidx referenced_size = %d, want %d",
			gopIndex, sidx.ReferencedSize, referenced)
	}
}
