package fmp4mux

import (
	"errors"
	"testing"
)

// TestRejectNonIDRStart verifies fragments cannot begin with a non-IDR AU.
func TestRejectNonIDRStart(t *testing.T) {
	f := NewFragmenter(Config{Timescale: testTimescale})
	_, _, err := f.PushSample(Sample{
		DTS: 0,
		AU:  annexB(nalBytes(1, 9)), // P slice, no IDR
	})
	if !errors.Is(err, ErrFragmentStart) {
		t.Fatalf("err = %v, want ErrFragmentStart", err)
	}
}

// TestPushAfterClose verifies Close finalizes the stream and rejects more input.
func TestPushAfterClose(t *testing.T) {
	f := NewFragmenter(Config{
		Timescale: testTimescale,
		SPS:       []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:       []byte{0x68, 0xce},
	})
	keyAU := annexB(nalBytes(nalTypeIDR, 1))
	if _, _, err := f.PushSample(Sample{DTS: 0, Key: true, AU: keyAU}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.PushSample(Sample{DTS: 3000, Key: true, AU: keyAU}); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

// TestNoBFramesUsesUnsignedTrun verifies trun v0 (unsigned, no CTO fields)
// when every composition offset is zero.
func TestNoBFramesUsesUnsignedTrun(t *testing.T) {
	cfg := Config{
		Timescale: testTimescale,
		SPS:       []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:       []byte{0x68, 0xce},
	}
	f := NewFragmenter(cfg)

	// First GOP.
	for i := 0; i < 3; i++ {
		s := Sample{DTS: uint64(i) * testFrameDur, AU: annexB(nalBytes(1, i))}
		if i == 0 {
			s.Key = true
			s.AU = annexB(nalBytes(nalTypeIDR, i))
		}
		if _, _, err := f.PushSample(s); err != nil {
			t.Fatal(err)
		}
	}
	// Second IDR flushes fragment 1.
	flushSeg, flushSIDX, err := f.PushSample(Sample{
		DTS: 9000, Key: true, AU: annexB(nalBytes(nalTypeIDR, 7)),
	})
	if err != nil {
		t.Fatal(err)
	}
	combined := append(append([]byte{}, flushSIDX...), flushSeg...)
	seg, err := ParseMediaSegment(combined)
	if err != nil {
		t.Fatal(err)
	}
	if seg.TrunVersion != 0 {
		t.Errorf("trun version = %d, want 0 without CTO", seg.TrunVersion)
	}
	// 0x800 (sample-composition-time-offsets present) must be clear.
	if seg.TrunFlags&0x000800 != 0 {
		t.Errorf("trun flags %x unexpectedly contain cto-present", seg.TrunFlags)
	}
	// Required flags still set: data-offset, duration, size, flags.
	for _, bit := range []uint32{0x001, 0x100, 0x200, 0x400} {
		if seg.TrunFlags&bit == 0 {
			t.Errorf("trun flags %x missing required bit %x", seg.TrunFlags, bit)
		}
	}
	for _, s := range seg.Samples {
		if s.CTO != 0 || s.PTS != int64(s.DTS) {
			t.Errorf("unexpected CTO/PTS: cto=%d pts=%d dts=%d", s.CTO, s.PTS, s.DTS)
		}
	}

	// Final fragment via Close.
	lastSeg, _, err := f.Close()
	if err != nil || lastSeg == nil {
		t.Fatalf("Close: seg=%v err=%v", lastSeg, err)
	}
}

// TestLengthPrefixedInput verifies AVCC (4-byte length prefixed) input is
// accepted and round-trips identically to Annex B input.
func TestLengthPrefixedInput(t *testing.T) {
	cfg := Config{
		Timescale: testTimescale,
		SPS:       []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:       []byte{0x68, 0xce},
	}
	f := NewFragmenter(cfg)
	nals := [][]byte{nalBytes(nalTypeIDR, 3), nalBytes(1, 4)}
	lpAU := lengthPrefixed(nals...)

	if _, _, err := f.PushSample(Sample{DTS: 0, Key: true, AU: lpAU}); err != nil {
		t.Fatal(err)
	}
	seg, sidx, err := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseMediaSegment(append(append([]byte{}, sidx...), seg...))
	if err != nil {
		t.Fatal(err)
	}
	want0 := toAVCSample(lpAU)
	if string(parsed.Samples[0].Data) != string(want0) {
		t.Error("length-prefixed sample did not round-trip")
	}

	// Annex B input with the same NAL units must convert identically.
	if got := toAVCSample(annexB(nals...)); string(got) != string(want0) {
		t.Error("Annex B and length-prefixed inputs converted differently")
	}
}

// TestVariableDurations verifies the last sample duration of a flushed
// fragment is derived from the next fragment's first DTS, even with irregular
// frame spacing.
func TestVariableDurations(t *testing.T) {
	cfg := Config{
		Timescale: testTimescale,
		SPS:       []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:       []byte{0x68, 0xce},
	}
	f := NewFragmenter(cfg)

	// Fragment 1: IDR at 0, P at 2500, P at 7000.
	frames := []struct {
		dts uint64
		key bool
	}{
		{0, true}, {2500, false}, {7000, false},
		{12000, true}, // next IDR -> flushes fragment 1
		{14000, false},
	}
	var flushSeg, flushSIDX []byte
	for i, fr := range frames {
		nal := nalBytes(1, i)
		if fr.key {
			nal = nalBytes(nalTypeIDR, i)
		}
		seg, sidx, err := f.PushSample(Sample{DTS: fr.dts, Key: fr.key, AU: annexB(nal)})
		if err != nil {
			t.Fatal(err)
		}
		if seg != nil {
			flushSeg, flushSIDX = seg, sidx
		}
	}

	combined := append(append([]byte{}, flushSIDX...), flushSeg...)
	seg, err := ParseMediaSegment(combined)
	if err != nil {
		t.Fatal(err)
	}
	wantDurations := []uint32{2500, 4500, 5000} // last one from next IDR DTS
	for i, want := range wantDurations {
		if got := sampleDuration(seg, i); got != want {
			t.Errorf("sample %d duration = %d, want %d", i, got, want)
		}
	}

	// Final fragment: last duration falls back to prior spacing (2000).
	lastSeg, lastSIDX, err := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	last, err := ParseMediaSegment(append(append([]byte{}, lastSIDX...), lastSeg...))
	if err != nil {
		t.Fatal(err)
	}
	if got := sampleDuration(last, len(last.Samples)-1); got != 2000 {
		t.Errorf("final last-sample duration = %d, want 2000", got)
	}
}

// TestExplicitParameterSets verifies the init segment is available before any
// sample when SPS/PPS are supplied in Config, and carries them in avcC.
func TestExplicitParameterSets(t *testing.T) {
	sps := []byte{0x67, 0x4d, 0x40, 0x1f}
	pps := []byte{0x68, 0xee, 0x3c, 0x80}
	f := NewFragmenter(Config{
		Timescale: testTimescale, Width: 640, Height: 480,
		SPS: sps, PPS: pps,
	})
	initSeg := f.InitSegment()
	if initSeg == nil {
		t.Fatal("init segment should be built eagerly with explicit SPS/PPS")
	}
	info, err := ParseInit(initSeg)
	if err != nil {
		t.Fatal(err)
	}
	wantAVCC := buildAVCC(sps, pps)
	if string(info.AVCC) != string(wantAVCC) {
		t.Error("avcC does not match configured parameter sets")
	}
}

// TestNegativePTSRejected verifies a fragment whose earliest presentation
// time is negative cannot be encoded into sidx (which is unsigned).
func TestNegativePTSRejected(t *testing.T) {
	cfg := Config{
		Timescale: testTimescale,
		SPS:       []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:       []byte{0x68, 0xce},
	}
	f := NewFragmenter(cfg)
	// IDR at DTS 0 with a negative composition offset -> negative PTS.
	if _, _, err := f.PushSample(Sample{
		DTS: 0, CTO: -3000, Key: true,
		AU: annexB(nalBytes(nalTypeIDR, 1)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Close(); !errors.Is(err, ErrNegativePTS) {
		t.Fatalf("Close err = %v, want ErrNegativePTS", err)
	}
}
