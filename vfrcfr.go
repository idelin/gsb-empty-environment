// Package vfrcfr 提供可变帧率（VFR）到固定帧率（CFR）的时间戳规整，
// 用于把手机录制的 VFR 视频整理成恒定帧率后送入编码器，并保持音画同步。
//
// 设计要点：
//   - 输出槽时刻用有理数精确计算（origin + n*step），不逐步累加，
//     因此不会因浮点或取整产生长期漂移，选择最近帧时误差天然不超过半帧。
//   - 重复时间戳只保留最后一帧；时间戳跳变超过阈值（默认 1 秒）视为新段，
//     段首帧重新对齐到输出槽 0。
//   - 音频按样本数推进（AudioSync），作为音画同步的基准时钟。
package vfrcfr

// Rational 表示一个有理数，用作帧率（帧/秒）。
type Rational struct {
	Num int64
	Den int64
}

// Frame 是一帧输入视频。ID 由调用方指定，用于追踪输出帧的来源。
type Frame struct {
	PTS int64 // 输入时间戳，单位：1/Timebase 秒
	ID  int
}

// OutFrame 是一个输出槽，即固定帧率下的一帧。
type OutFrame struct {
	Slot       int64 // 输出槽序号，全局递增
	PTS        int64 // 输出时间戳，单位：1/Timebase 秒
	SrcID      int   // 来源输入帧的 ID
	Duplicated bool  // 该槽复用了上一槽的源帧（输入帧率不足时复制）
}

// VideoRegularizer 把可变帧率的输入帧映射到固定帧率的输出槽。
type VideoRegularizer struct {
	FPS        Rational // 目标帧率
	Timebase   int64    // 时间基，每秒的 tick 数
	JumpThresh int64    // 跳变阈值（tick），<= 0 时默认为 1 秒

	origin  int64 // 当前段槽 0 的时刻（tick）
	segSlot int64 // 当前段内下一个输出槽
	slot    int64 // 全局输出槽序号
	buf     []Frame
	prev    Frame // 上一输出槽的源帧，作为后续槽的候选（实现复制）
	lastPTS int64
	hasLast bool
	hasPrev bool

	Dropped        int // 被更邻近帧挤掉而丢弃的输入帧数
	LateDuplicates int // 去重时源帧已被消费、只能丢弃的重复帧数
	OutOfOrder     int // 小幅回退（乱序）被丢弃的帧数
}

// NewVideoRegularizer 创建规整器。fps 为目标帧率，timebase 为时间基
// （如 90000 表示 90kHz）。
func NewVideoRegularizer(fps Rational, timebase int64) *VideoRegularizer {
	return &VideoRegularizer{FPS: fps, Timebase: timebase}
}

func (v *VideoRegularizer) jumpThresh() int64 {
	if v.JumpThresh > 0 {
		return v.JumpThresh
	}
	return v.Timebase // 默认 1 秒
}

// step 返回输出槽间隔：slotTime = origin + n*num/den（tick）。
func (v *VideoRegularizer) step() (num, den int64) {
	return v.Timebase * v.FPS.Den, v.FPS.Num
}

// Feed 送入一帧输入，返回本次可以确定下来的输出帧（可能为 0 个或多个）。
// 输入时间戳应大致单调递增；完全相等的时间戳会去重（保留最后一帧），
// 与上一帧相差超过 JumpThresh 时视为新段并重新对齐。
func (v *VideoRegularizer) Feed(pts int64, id int) []OutFrame {
	var out []OutFrame
	if !v.hasLast {
		v.origin = pts // 首帧对齐到槽 0
	} else {
		d := pts - v.lastPTS
		thresh := v.jumpThresh()
		switch {
		case d == 0:
			// 重复时间戳：只保留最后一帧
			if n := len(v.buf); n > 0 && v.buf[n-1].PTS == pts {
				v.buf[n-1].ID = id
			} else {
				v.LateDuplicates++
			}
			return nil
		case d > thresh || d < -thresh:
			// 跳变超过阈值：结束当前段，以新帧为原点重新对齐
			v.emit(true, &out)
			v.buf = v.buf[:0]
			v.origin = pts
			v.segSlot = 0
			v.hasPrev = false
		case d < 0:
			v.OutOfOrder++
			return nil
		}
	}
	v.buf = append(v.buf, Frame{PTS: pts, ID: id})
	v.lastPTS, v.hasLast = pts, true
	v.emit(false, &out)
	return out
}

// Flush 在输入结束时调用，吐出缓冲区中剩余帧对应的输出。
func (v *VideoRegularizer) Flush() []OutFrame {
	var out []OutFrame
	v.emit(true, &out)
	return out
}

// emit 尽可能多地确定输出槽。final 为 false 时，只有当缓冲区里出现了
// 严格晚于槽中心的帧（此时最近帧已确定，后续帧只会更远）才输出；
// final 为 true 时强制输出，并在槽中心距末帧不超过半帧时复制末帧收尾。
func (v *VideoRegularizer) emit(final bool, out *[]OutFrame) {
	num, den := v.step()
	half := num / 2 // 半帧，单位 1/den tick
	for {
		t := v.origin*den + v.segSlot*num // 槽中心时刻，单位 1/den tick
		// 找第一个严格晚于槽中心的帧
		idx := 0
		for idx < len(v.buf) && v.buf[idx].PTS*den <= t {
			idx++
		}
		if len(v.buf) == 0 {
			// 尾部：仅在 final 且槽中心距末帧不超过半帧时复制末帧
			if !final || !v.hasPrev || t-v.prev.PTS*den > half {
				return
			}
			v.emitFrame(v.prev, out)
			continue
		}
		if idx == len(v.buf) && !final {
			return // 还没有帧越过槽中心，最近帧尚未确定
		}
		// 候选：中心之前最近的帧（buf[idx-1]，没有则是上一槽源帧 prev）
		// 与中心之后最近的帧（buf[idx]），取距离更近者，平局取前者。
		var before *Frame
		if idx > 0 {
			before = &v.buf[idx-1]
		} else if v.hasPrev {
			before = &v.prev
		}
		chosenIdx := -1 // 选中帧在 buf 中的下标，-1 表示 prev
		switch {
		case before == nil:
			chosenIdx = idx
		case idx == len(v.buf):
			chosenIdx = idx - 1
		case v.buf[idx].PTS*den-t < t-before.PTS*den:
			chosenIdx = idx
		case idx > 0:
			chosenIdx = idx - 1
		}
		if chosenIdx >= 0 {
			v.Dropped += chosenIdx // 被更邻近帧挤掉的输入帧
			v.emitFrame(v.buf[chosenIdx], out)
			v.buf = v.buf[chosenIdx+1:]
		} else {
			v.emitFrame(v.prev, out)
		}
	}
}

// emitFrame 输出一个槽，源帧为 f。
func (v *VideoRegularizer) emitFrame(f Frame, out *[]OutFrame) {
	num, den := v.step()
	t := v.origin*den + v.segSlot*num
	dup := v.hasPrev && v.prev.ID == f.ID
	*out = append(*out, OutFrame{
		Slot:       v.slot,
		PTS:        roundDiv(t, den),
		SrcID:      f.ID,
		Duplicated: dup,
	})
	v.prev, v.hasPrev = f, true
	v.segSlot++
	v.slot++
}

// roundDiv 计算 a/b 四舍五入到整数，支持负数。
func roundDiv(a, b int64) int64 {
	if a >= 0 {
		return (a + b/2) / b
	}
	return -(-a + b/2) / b
}

// AudioSync 以样本数为基准推进音频时钟，作为音画同步的参考。
// 音频 PTS 永远由“已消耗样本数 / 采样率”导出，不依赖不稳定的时间戳。
type AudioSync struct {
	SampleRate int64
	Timebase   int64
	samples    int64 // 已消耗的样本数
}

// NewAudioSync 创建音频同步基准时钟。
func NewAudioSync(sampleRate, timebase int64) *AudioSync {
	return &AudioSync{SampleRate: sampleRate, Timebase: timebase}
}

// Push 消耗 n 个样本，返回该音频块起始时刻的时间戳（tick）。
func (a *AudioSync) Push(n int64) int64 {
	pts := a.Position()
	a.samples += n
	return pts
}

// Position 返回当前音频位置（tick，四舍五入到最近 tick）。
func (a *AudioSync) Position() int64 {
	return (a.samples*a.Timebase + a.SampleRate/2) / a.SampleRate
}

// Drift 返回给定视频时间戳相对当前音频位置的偏差（tick）。
func (a *AudioSync) Drift(videoPTS int64) int64 {
	return videoPTS - a.Position()
}
