package main

import (
	"math/big"
	"testing"
)

func mustRat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rat %q", s)
	}
	return r
}

func TestRoundHalfEven(t *testing.T) {
	cases := map[string]string{
		"1/2": "0", "3/2": "2", "5/2": "2", "7/2": "4",
		"-1/2": "0", "-3/2": "-2", "-5/2": "-2", "-7/2": "-4",
		"1/3": "0", "2/3": "1", "10": "10", "-2/3": "-1",
	}
	for in, want := range cases {
		if got := roundHalfEven(mustRat(t, in)); got.String() != want {
			t.Errorf("roundHalfEven(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestMappingForwardInverse(t *testing.T) {
	pm := &PiecewiseMap{Knots: []Knot{
		{T: mustRat(t, "0"), M: mustRat(t, "0")},
		{T: mustRat(t, "10"), M: mustRat(t, "10")},
		{T: mustRat(t, "100"), M: mustRat(t, "19")},
		{T: mustRat(t, "200"), M: mustRat(t, "109")},
	}}
	if got := pm.Forward(mustRat(t, "55")); got.Cmp(mustRat(t, "29/2")) != 0 {
		t.Errorf("Forward(55) = %s, want 29/2", got)
	}
	if got := pm.Inverse(mustRat(t, "29/2")); got.Cmp(mustRat(t, "55")) != 0 {
		t.Errorf("Inverse(29/2) = %s, want 55", got)
	}
	for _, s := range []string{"0", "10", "55", "100", "200", "300", "-5", "1/3"} {
		x := mustRat(t, s)
		if got := pm.Inverse(pm.Forward(x)); got.Cmp(x) != 0 {
			t.Errorf("round trip %s: got %s", s, got)
		}
	}
	if err := pm.validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func sampleSpec(t *testing.T) *Spec {
	mk := func(s string) Rat { return Rat{mustRat(t, s)} }
	return &Spec{
		Config: Config{MinIdle: mk("5"), Ratio: mk("1/10"), MinKeep: mk("1")},
		Events: []Event{
			{T: mk("0"), Track: "video", Kind: "cut"},
			{T: mk("10"), Track: "video", Kind: "cut"},
			{T: mk("10"), Track: "audio", Kind: "marker"}, // 同刻事件
			{T: mk("100"), Track: "video", Kind: "cut"},
			{T: mk("110"), Track: "video", Kind: "cut"},
			{T: mk("200"), Track: "video", Kind: "cut"},
		},
		Protected: []Protected{
			{Start: mk("55"), End: mk("65"), Kind: "audio-continuous"},
			{Start: mk("150"), End: mk("160"), Kind: "mouse-drag"},
		},
		Tracks: []Track{
			{ID: "video", Start: mk("0"), Interval: mk("1/30")},
			{ID: "audio", Start: mk("1/2"), Interval: mk("1/48000")},
		},
		SyncPoints: []Rat{mk("10"), mk("100"), mk("175")},
	}
}

func TestPlanCompression(t *testing.T) {
	spec := sampleSpec(t)
	plan, err := planCompression(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Map.validate(); err != nil {
		t.Fatal(err)
	}
	// 6 段可压缩：(0,10) (10,55) (65,100) (100,110) (110,150) (160,200)
	if len(plan.Segments) != 6 {
		t.Fatalf("segments = %d, want 6", len(plan.Segments))
	}
	for _, s := range plan.Segments {
		for _, p := range spec.Protected {
			if interiorOverlap(s.Start, s.End, p.Start.R, p.End.R) {
				t.Errorf("压缩段[%s,%s] 与受保护段[%s,%s] 重叠",
					s.Start, s.End, p.Start, p.End)
			}
		}
	}
	// 受保护段不被压缩：映射前后等长
	for _, p := range spec.Protected {
		origLen := rsub(p.End.R, p.Start.R)
		mappedLen := rsub(plan.Map.Forward(p.End.R), plan.Map.Forward(p.Start.R))
		if origLen.Cmp(mappedLen) != 0 {
			t.Errorf("受保护段[%s,%s] 长度被改变: %s -> %s",
				p.Start, p.End, origLen, mappedLen)
		}
	}
	// 同刻事件映射后仍同刻
	m1 := plan.Map.Forward(mustRat(t, "10"))
	m2 := plan.Map.Forward(mustRat(t, "10"))
	if m1.Cmp(m2) != 0 {
		t.Errorf("同刻事件映射后不同刻: %s vs %s", m1, m2)
	}
}

func TestReportVerification(t *testing.T) {
	spec := sampleSpec(t)
	plan, err := planCompression(spec)
	if err != nil {
		t.Fatal(err)
	}
	rep := buildReport(spec, plan)
	if !rep.Verification.OK {
		t.Fatalf("verification failed: %+v", rep.Verification)
	}
	for _, sp := range rep.SyncPoints {
		if sp.ReverseLo.R.Cmp(sp.SyncTime.R) > 0 || sp.ReverseHi.R.Cmp(sp.SyncTime.R) < 0 {
			t.Errorf("同步点 %s 不在反向区间 [%s,%s]",
				sp.SyncTime, sp.ReverseLo, sp.ReverseHi)
		}
		for _, w := range sp.Windows {
			if !w.ContainsSync {
				t.Errorf("同步点 %s 不在轨道 %s 的帧窗口 [%s,%s]",
					sp.SyncTime, w.Track, w.OrigWindowLo, w.OrigWindowHi)
			}
		}
	}
	if len(rep.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(rep.Tracks))
	}
	for _, tr := range rep.Tracks {
		if len(tr.Evidence) == 0 {
			t.Errorf("轨道 %s 缺少偏移证据", tr.ID)
		}
	}
}

func TestFrameShiftEvidence(t *testing.T) {
	spec := sampleSpec(t)
	plan, err := planCompression(spec)
	if err != nil {
		t.Fatal(err)
	}
	rep := buildReport(spec, plan)
	// 视频轨：t=200 处原帧 6000，映射后帧应减少（空闲段被压缩）
	video := rep.Tracks[0]
	found := false
	for _, ev := range video.Evidence {
		if ev.Point == "t=200" {
			found = true
			if ev.OrigFrame != "6000" {
				t.Errorf("t=200 原帧 = %s, want 6000", ev.OrigFrame)
			}
			if ev.FrameShift == "0" {
				t.Errorf("t=200 帧偏移不应为 0")
			}
		}
	}
	if !found {
		t.Error("缺少 t=200 检查点证据")
	}
}
