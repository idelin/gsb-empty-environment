package vfrcfr

import "testing"

const tb = 90000 // 90kHz 时间基

// feedAll 依次送入所有帧并收集全部输出（含 Flush）。
func feedAll(v *VideoRegularizer, frames []Frame) []OutFrame {
	var out []OutFrame
	for _, f := range frames {
		out = append(out, v.Feed(f.PTS, f.ID)...)
	}
	return append(out, v.Flush()...)
}

// constFrames 生成等间隔帧序列，起始 start，间隔 step（tick）。
func constFrames(start, step int64, n int) []Frame {
	fs := make([]Frame, n)
	for i := range fs {
		fs[i] = Frame{PTS: start + int64(i)*step, ID: i}
	}
	return fs
}

// nearestSrc 独立重算：返回离 center（单位 1/den tick）最近的帧的 ID。
// frames 按 PTS 升序，距离开始变大时提前结束。
func nearestSrc(frames []Frame, center, den int64) int {
	best, bestDist := -1, int64(-1)
	for _, f := range frames {
		d := f.PTS*den - center
		if d < 0 {
			d = -d
		}
		if bestDist >= 0 && d > bestDist {
			break
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = f.ID, d
		}
	}
	return best
}

// TestConstantRatePassthrough 恒定 30fps 输入 → 30fps 输出，一一对应、零误差。
func TestConstantRatePassthrough(t *testing.T) {
	v := NewVideoRegularizer(Rational{30, 1}, tb)
	frames := constFrames(0, 3000, 300)
	out := feedAll(v, frames)
	if len(out) != len(frames) {
		t.Fatalf("输出帧数 = %d，期望 %d", len(out), len(frames))
	}
	for i, o := range out {
		if o.SrcID != i || o.Duplicated {
			t.Fatalf("槽 %d 来源 = %d (dup=%v)，期望 %d", i, o.SrcID, o.Duplicated, i)
		}
		if o.PTS != int64(i)*3000 {
			t.Fatalf("槽 %d PTS = %d，期望 %d", i, o.PTS, i*3000)
		}
	}
	if v.Dropped != 0 {
		t.Fatalf("Dropped = %d，期望 0", v.Dropped)
	}
}

// TestDuplicatePTSKeepLast 重复时间戳只保留最后一帧。
func TestDuplicatePTSKeepLast(t *testing.T) {
	v := NewVideoRegularizer(Rational{30, 1}, tb)
	frames := []Frame{
		{PTS: 0, ID: 0},
		{PTS: 0, ID: 1}, // 与上一帧同 PTS，应覆盖
		{PTS: 3000, ID: 2},
		{PTS: 3000, ID: 3}, // 覆盖 ID 2
		{PTS: 6000, ID: 4},
	}
	out := feedAll(v, frames)
	wantSrc := []int{1, 3, 4}
	if len(out) != len(wantSrc) {
		t.Fatalf("输出帧数 = %d，期望 %d", len(out), len(wantSrc))
	}
	for i, o := range out {
		if o.SrcID != wantSrc[i] {
			t.Fatalf("槽 %d 来源 = %d，期望 %d", i, o.SrcID, wantSrc[i])
		}
	}
}

// TestSegmentJumpRealign 跳变超过一秒视为新段，段首帧重新对齐到槽。
func TestSegmentJumpRealign(t *testing.T) {
	v := NewVideoRegularizer(Rational{30, 1}, tb)
	frames := constFrames(0, 3000, 3) // 0, 3000, 6000
	// 跳到约 5.56 秒处，远超 1 秒阈值
	frames = append(frames, constFrames(500000, 3000, 3)...)
	for i, f := range frames {
		f.ID = i
		frames[i] = f
	}
	out := feedAll(v, frames)
	if len(out) != 6 {
		t.Fatalf("输出帧数 = %d，期望 6", len(out))
	}
	// 新段第一帧（PTS=500000）必须精确落在槽上，误差为 0
	if out[3].PTS != 500000 || out[3].SrcID != 3 {
		t.Fatalf("新段首槽 PTS=%d Src=%d，期望 PTS=500000 Src=3", out[3].PTS, out[3].SrcID)
	}
	for i, want := range []int64{0, 3000, 6000, 500000, 503000, 506000} {
		if out[i].PTS != want {
			t.Fatalf("槽 %d PTS = %d，期望 %d", i, out[i].PTS, want)
		}
	}
}

// TestVFRBurstyInput 模拟手机录制：帧间隔在 2000/4000 tick 间交替
// （平均 30fps），核对每个输出槽都取了最近帧，且误差不超过半帧。
func TestVFRBurstyInput(t *testing.T) {
	v := NewVideoRegularizer(Rational{30, 1}, tb)
	var frames []Frame
	pts := int64(0)
	for i := 0; i < 400; i++ {
		frames = append(frames, Frame{PTS: pts, ID: i})
		if i%2 == 0 {
			pts += 2000
		} else {
			pts += 4000
		}
	}
	out := feedAll(v, frames)
	num, den := v.step()
	half := num / 2 // 半帧，单位 1/den tick
	for _, o := range out {
		center := o.Slot * num // origin = 0
		if want := nearestSrc(frames, center, den); o.SrcID != want {
			t.Fatalf("槽 %d 来源 = %d，独立重算期望 %d", o.Slot, o.SrcID, want)
		}
		src := frames[o.SrcID].PTS * den
		if d := src - center; d > half || d < -half {
			t.Fatalf("槽 %d 误差 %d 超过半帧 %d", o.Slot, d, half)
		}
	}
	// 平均帧率一致，输出数应与输入数接近（差 1 以内）
	if diff := len(out) - len(frames); diff < -1 || diff > 1 {
		t.Fatalf("输出 %d 帧 vs 输入 %d 帧，偏差过大", len(out), len(frames))
	}
}

// TestLowRateDuplicates 10fps 输入 → 30fps 输出，每帧应被复制，
// 每个槽的源帧都是独立重算的最近帧。
func TestLowRateDuplicates(t *testing.T) {
	v := NewVideoRegularizer(Rational{30, 1}, tb)
	frames := constFrames(0, 9000, 10)
	out := feedAll(v, frames)
	if len(out) != 28 { // 槽 0..27，槽 27 中心恰好落在末帧上
		t.Fatalf("输出帧数 = %d，期望 28", len(out))
	}
	for i, o := range out {
		if want := (i + 1) / 3; o.SrcID != want {
			t.Fatalf("槽 %d 来源 = %d，期望最近帧 %d", i, o.SrcID, want)
		}
	}
	// 帧 0 应出现在槽 0、1（槽 1 中心 3000，距帧 0 为 3000 < 距帧 1 的 6000）
	if out[0].SrcID != 0 || out[1].SrcID != 0 || !out[1].Duplicated {
		t.Fatalf("槽 0/1 来源 = %d/%d (dup=%v)，期望 0/0 (dup)",
			out[0].SrcID, out[1].SrcID, out[1].Duplicated)
	}
	if v.Dropped != 0 {
		t.Fatalf("Dropped = %d，期望 0", v.Dropped)
	}
}

// TestHighRateDrops 60fps 输入 → 30fps 输出，每隔一帧丢弃。
func TestHighRateDrops(t *testing.T) {
	v := NewVideoRegularizer(Rational{30, 1}, tb)
	frames := constFrames(0, 1500, 200)
	out := feedAll(v, frames)
	// 槽 0..99 取偶数帧；末帧（PTS 298500）距槽 100 中心恰为半帧，收尾保留
	if len(out) != 101 {
		t.Fatalf("输出帧数 = %d，期望 101", len(out))
	}
	for i, o := range out {
		want := 2 * i
		if i == 100 {
			want = 199
		}
		if o.SrcID != want {
			t.Fatalf("槽 %d 来源 = %d，期望 %d", i, o.SrcID, want)
		}
	}
	if v.Dropped != 99 {
		t.Fatalf("Dropped = %d，期望 99", v.Dropped)
	}
}

// TestLongTermDrift 23.976fps（24000/1001，帧间隔 3753.75 tick 非整数）
// 下送入 10000 帧，输入 PTS 带每帧取整误差；核对每个输出帧来源，
// 并验证长期漂移不超过半帧、以音频样本数为基准的音画偏差不超限。
func TestLongTermDrift(t *testing.T) {
	fps := Rational{24000, 1001}
	v := NewVideoRegularizer(fps, tb)
	const n = 10000
	// 输入帧 PTS：理想时刻 k*1001*90000/24000 四舍五入到整数 tick
	var frames []Frame
	for k := 0; k < n; k++ {
		pts := (int64(k)*1001*tb + 12000) / 24000
		frames = append(frames, Frame{PTS: pts, ID: k})
	}
	out := feedAll(v, frames)
	if len(out) != n {
		t.Fatalf("输出帧数 = %d，期望 %d", len(out), n)
	}
	num, den := v.step()
	half := num / 2
	var maxErr int64
	for _, o := range out {
		center := o.Slot * num
		if want := nearestSrc(frames, center, den); o.SrcID != want {
			t.Fatalf("槽 %d 来源 = %d，独立重算期望 %d", o.Slot, o.SrcID, want)
		}
		if d := frames[o.SrcID].PTS*den - center; d > half || d < -half {
			t.Fatalf("槽 %d 误差 %d 超过半帧 %d", o.Slot, d, half)
		} else {
			if d < 0 {
				d = -d
			}
			if d > maxErr {
				maxErr = d
			}
		}
	}
	t.Logf("10000 槽最大源帧偏差 = %d/%d tick（半帧上限 %d/%d）",
		maxErr, den, half, den)

	// 音频基准：每输出帧对应 48000*1001/24000 = 2002 个样本，
	// 按累计样本数推进，最终音画偏差应远小于半帧。
	a := NewAudioSync(48000, tb)
	var prev int64
	for k := range out {
		total := (int64(k+1) * 48000 * 1001) / 24000
		a.Push(total - prev)
		prev = total
	}
	last := out[len(out)-1]
	endPTS := last.PTS + num/den // 最后一帧结束时刻
	if d := a.Drift(endPTS); d > half/den+2 || d < -(half/den+2) {
		t.Fatalf("末端音画偏差 = %d tick，超出半帧量级", d)
	}
}

// TestAudioSyncBasics 音频时钟按样本数推进。
func TestAudioSyncBasics(t *testing.T) {
	a := NewAudioSync(48000, tb)
	if pts := a.Push(48000); pts != 0 {
		t.Fatalf("首块 PTS = %d，期望 0", pts)
	}
	if pos := a.Position(); pos != tb {
		t.Fatalf("1 秒后位置 = %d，期望 %d", pos, tb)
	}
	// 1601.6 样本/帧 的取整推进，1000 帧后仍应紧贴
	a2 := NewAudioSync(48000, tb)
	var prev int64
	for k := 0; k < 1000; k++ {
		total := (int64(k+1) * 48000 * 1001) / 30000
		a2.Push(total - prev)
		prev = total
	}
	ideal := int64(1000) * 1001 * tb / 30000
	if d := a2.Position() - ideal; d > 1 || d < -1 {
		t.Fatalf("1000 帧后音频位置偏差 = %d tick", d)
	}
}
