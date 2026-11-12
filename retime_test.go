package taskpkg

import (
	"math/big"
	"testing"
)

func rat(t *testing.T, value string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		t.Fatalf("invalid rational %q", value)
	}
	return r
}

func segment(t *testing.T, targetStart, targetEnd, sourceStart, sourceEnd string) Segment {
	t.Helper()
	return Segment{
		TargetStart: rat(t, targetStart),
		TargetEnd:   rat(t, targetEnd),
		SourceStart: rat(t, sourceStart),
		SourceEnd:   rat(t, sourceEnd),
	}
}

func assertRat(t *testing.T, got, want *big.Rat) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("rational mismatch: got %v, want %v", got, want)
		}
		return
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("rational mismatch: got %s, want %s", got.RatString(), want.RatString())
	}
}

func assertIntervals(t *testing.T, got []Interval, want [][2]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("interval count mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		assertRat(t, got[i].Start, rat(t, want[i][0]))
		assertRat(t, got[i].End, rat(t, want[i][1]))
	}
}

func assertCues(t *testing.T, got []Cue, want []Cue) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("cue count mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Fatalf("cue %d ID mismatch: got %q, want %q", i, got[i].ID, want[i].ID)
		}
		if got[i].Text != want[i].Text {
			t.Fatalf("cue %d text mismatch: got %q, want %q", i, got[i].Text, want[i].Text)
		}
		assertRat(t, got[i].Start, want[i].Start)
		assertRat(t, got[i].End, want[i].End)
	}
}

func TestRetimeReverseFreezeAndRepeatedOccurrence(t *testing.T) {
	mapping, err := NewTimeMap([]Segment{
		segment(t, "0", "2", "10", "8"), // reverse
		segment(t, "2", "4", "8", "8"),  // freeze
		segment(t, "4", "6", "8", "10"), // forward
	})
	if err != nil {
		t.Fatalf("NewTimeMap: %v", err)
	}

	spanning := Subtitle{
		ID:    "sub-spanning",
		Text:  "跨过倒放和停帧",
		Start: rat(t, "7/2"),
		End:   rat(t, "9"),
	}
	if got := mapping.VisibleIntervals(spanning); len(got) != 1 ||
		got[0].Start.Cmp(rat(t, "1")) != 0 || got[0].End.Cmp(rat(t, "5")) != 0 {
		t.Fatalf("spanning intervals = %v, want [[1,5)]", got)
	}

	repeated := Subtitle{
		ID:    "sub-repeat",
		Text:  "同一字幕出现两次",
		Start: rat(t, "9"),
		End:   rat(t, "10"),
	}
	assertIntervals(t, mapping.VisibleIntervals(repeated), [][2]string{
		{"0", "1"},
		{"5", "6"},
	})
	assertCues(t, mapping.Retime(repeated), []Cue{
		{ID: "sub-repeat", Text: "同一字幕出现两次", Start: rat(t, "0"), End: rat(t, "1")},
		{ID: "sub-repeat", Text: "同一字幕出现两次", Start: rat(t, "5"), End: rat(t, "6")},
	})

	cues := mapping.RetimeAll([]Subtitle{spanning, repeated})
	assertCues(t, cues, []Cue{
		{ID: "sub-repeat", Text: "同一字幕出现两次", Start: rat(t, "0"), End: rat(t, "1")},
		{ID: "sub-spanning", Text: "跨过倒放和停帧", Start: rat(t, "1"), End: rat(t, "5")},
		{ID: "sub-repeat", Text: "同一字幕出现两次", Start: rat(t, "5"), End: rat(t, "6")},
	})
}

func TestReversePreimageHasExactHalfOpenBoundaries(t *testing.T) {
	mapping, err := NewTimeMap([]Segment{
		segment(t, "0", "1", "1", "0"),
	})
	if err != nil {
		t.Fatalf("NewTimeMap: %v", err)
	}

	subtitle := Subtitle{ID: "r", Text: "reverse", Start: rat(t, "0"), End: rat(t, "1/2")}
	assertIntervals(t, mapping.VisibleIntervals(subtitle), [][2]string{{"1/2", "1"}})

	whole := Subtitle{ID: "w", Text: "whole", Start: rat(t, "0"), End: rat(t, "1")}
	assertIntervals(t, mapping.VisibleIntervals(whole), [][2]string{{"0", "1"}})
}

func TestFreezeRemainsVisibleForEntireTargetSpan(t *testing.T) {
	mapping, err := NewTimeMap([]Segment{
		segment(t, "0", "10", "5", "5"),
	})
	if err != nil {
		t.Fatalf("NewTimeMap: %v", err)
	}

	subtitle := Subtitle{ID: "freeze", Text: "held frame", Start: rat(t, "4"), End: rat(t, "6")}
	assertIntervals(t, mapping.VisibleIntervals(subtitle), [][2]string{{"0", "10"}})

	outside := Subtitle{ID: "outside", Text: "not visible", Start: rat(t, "6"), End: rat(t, "8")}
	assertIntervals(t, mapping.VisibleIntervals(outside), nil)
}

func TestRetimeGlobalOrderingAndZeroDuration(t *testing.T) {
	mapping, err := NewTimeMap([]Segment{
		segment(t, "0", "10", "0", "10"),
	})
	if err != nil {
		t.Fatalf("NewTimeMap: %v", err)
	}

	subtitles := []Subtitle{
		{ID: "b", Text: "B", Start: rat(t, "1"), End: rat(t, "4")},
		{ID: "a", Text: "A", Start: rat(t, "2"), End: rat(t, "3")},
		{ID: "z", Text: "zero", Start: rat(t, "2"), End: rat(t, "2")},
		{ID: "c", Text: "C", Start: rat(t, "1"), End: rat(t, "2")},
	}

	assertCues(t, mapping.RetimeAll(subtitles), []Cue{
		{ID: "b", Text: "B", Start: rat(t, "1"), End: rat(t, "4")},
		{ID: "c", Text: "C", Start: rat(t, "1"), End: rat(t, "2")},
		{ID: "a", Text: "A", Start: rat(t, "2"), End: rat(t, "3")},
	})

	grouped := mapping.RetimeSubtitles(subtitles)
	if len(grouped) != 3 {
		t.Fatalf("grouped subtitle count = %d, want 3", len(grouped))
	}
	for _, group := range grouped {
		if group.ID == "z" {
			t.Fatalf("zero-duration subtitle was returned")
		}
	}
	assertCues(t, Flatten(grouped), mapping.RetimeAll(subtitles))
}

func TestRetimeAcrossManyNonTerminatingRationalSegments(t *testing.T) {
	const pieceCount = 997
	base := new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 80))
	step := rat(t, "1/7")

	segments := make([]Segment, pieceCount)
	for i := 0; i < pieceCount; i++ {
		start := new(big.Rat).Add(base, new(big.Rat).Mul(step, big.NewRat(int64(i), 1)))
		end := new(big.Rat).Add(start, step)
		segments[i] = Segment{
			TargetStart: start,
			TargetEnd:   end,
			SourceStart: new(big.Rat).Set(start),
			SourceEnd:   new(big.Rat).Set(end),
		}
	}

	mapping, err := NewTimeMap(segments)
	if err != nil {
		t.Fatalf("NewTimeMap: %v", err)
	}

	start := new(big.Rat).Add(base, new(big.Rat).Mul(step, big.NewRat(123, 1)))
	end := new(big.Rat).Add(base, new(big.Rat).Mul(step, big.NewRat(955, 1)))
	subtitle := Subtitle{ID: "exact", Text: "rational", Start: start, End: end}

	assertIntervals(t, mapping.VisibleIntervals(subtitle), [][2]string{
		{start.RatString(), end.RatString()},
	})

	// The reference endpoint is rebuilt directly from integers and cannot
	// have accumulated 996 additions of an inexact binary fraction.
	expectedStart := new(big.Rat).Add(
		new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 80)),
		big.NewRat(123, 7),
	)
	expectedEnd := new(big.Rat).Add(
		new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 80)),
		big.NewRat(955, 7),
	)
	assertRat(t, mapping.VisibleIntervals(subtitle)[0].Start, expectedStart)
	assertRat(t, mapping.VisibleIntervals(subtitle)[0].End, expectedEnd)
}

func TestSourceAtAndDomain(t *testing.T) {
	mapping, err := NewTimeMap([]Segment{
		segment(t, "0", "2", "10", "8"),
		segment(t, "2", "4", "8", "8"),
		segment(t, "4", "6", "8", "10"),
	})
	if err != nil {
		t.Fatalf("NewTimeMap: %v", err)
	}

	tests := []struct {
		target string
		source string
	}{
		{"0", "10"},
		{"1", "9"},
		{"2", "8"},
		{"3", "8"},
		{"4", "8"},
		{"5", "9"},
		{"6", "10"},
	}
	for _, test := range tests {
		got, err := mapping.SourceAt(rat(t, test.target))
		if err != nil {
			t.Fatalf("SourceAt(%s): %v", test.target, err)
		}
		assertRat(t, got, rat(t, test.source))
	}

	if _, err := mapping.SourceAt(rat(t, "7")); err == nil {
		t.Fatal("SourceAt outside the target domain unexpectedly succeeded")
	}
	start, end := mapping.TargetDomain()
	assertRat(t, start, rat(t, "0"))
	assertRat(t, end, rat(t, "6"))
}

func TestNewTimeMapRejectsInvalidSegments(t *testing.T) {
	tests := []struct {
		name     string
		segments []Segment
	}{
		{
			name: "empty",
		},
		{
			name: "nil endpoint",
			segments: []Segment{{
				TargetStart: rat(t, "0"),
				TargetEnd:   rat(t, "1"),
				SourceStart: rat(t, "0"),
			}},
		},
		{
			name: "zero target duration",
			segments: []Segment{{
				TargetStart: rat(t, "1"),
				TargetEnd:   rat(t, "1"),
				SourceStart: rat(t, "0"),
				SourceEnd:   rat(t, "1"),
			}},
		},
		{
			name: "target gap",
			segments: []Segment{
				segment(t, "0", "1", "0", "1"),
				segment(t, "3/2", "2", "1", "2"),
			},
		},
		{
			name: "source discontinuity",
			segments: []Segment{
				segment(t, "0", "1", "0", "1"),
				segment(t, "1", "2", "2", "3"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if mapping, err := NewTimeMap(test.segments); err == nil {
				t.Fatalf("NewTimeMap unexpectedly succeeded: %+v", mapping)
			}
		})
	}
}
