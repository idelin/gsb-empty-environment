package timemap

import (
	"math/big"
	"testing"
)

func TestReverseFrame(t *testing.T) {
	m := sampleMapping(t)
	tr := Track{ID: "v", Start: RatFromInt(0), Interval: NewRat(1, 30)}
	// frame 60: bucket [119/60, 121/60) on compressed timeline; both ends lie
	// in the slope-1/5 piece [0,10)->[0,2), so inverse multiplies by 5.
	lo, hi, err := tr.ReverseFrame(m, big.NewInt(60))
	if err != nil {
		t.Fatal(err)
	}
	if lo.String() != "119/12" || hi.String() != "601/60" {
		t.Errorf("ReverseFrame(60) = [%s, %s)", lo, hi)
	}
}

func TestCrossTrackSync(t *testing.T) {
	m := sampleMapping(t)
	tracks := []Track{
		{ID: "video", Start: RatFromInt(0), Interval: NewRat(1, 30)},
		{ID: "audio", Start: RatFromInt(0), Interval: NewRat(1, 48000)},
	}
	sc := CheckSyncPoint(m, tracks, RatFromInt(5))
	if !sc.OK {
		t.Fatalf("sync check failed: %+v", sc)
	}
	if sc.Compressed.String() != "1" {
		t.Errorf("compressed time = %s, want 1", sc.Compressed)
	}
	if sc.PerTrack[0].Frame != "30" {
		t.Errorf("video frame = %s, want 30", sc.PerTrack[0].Frame)
	}
	if sc.PerTrack[1].Frame != "48000" {
		t.Errorf("audio frame = %s, want 48000", sc.PerTrack[1].Frame)
	}
}

func TestEvidenceShift(t *testing.T) {
	m := sampleMapping(t)
	tracks := []Track{{ID: "v", Start: RatFromInt(10), Interval: NewRat(1, 30)}}
	ev, err := Evidence(tracks, m)
	if err != nil {
		t.Fatal(err)
	}
	if ev[0].MappedStart.String() != "2" || ev[0].Shift.String() != "-8" {
		t.Errorf("evidence = %+v", ev[0])
	}
}
