package tsdemux

// MPEG-TS 时间戳模数。
// PTS / DTS 为 33 位、90kHz 时钟；PCR 为 42 位（33 位 90kHz 基准 +
// 9 位 27MHz 扩展），整体按 27MHz 计。
const (
	mod33 uint64 = 1 << 33
	// PCR 在 27MHz 域的回绕周期：33 位 base 每拍对应 300 个 27MHz 时钟。
	modPCR  uint64 = mod33 * 300
	half33  uint64 = mod33 / 2
	halfPCR uint64 = modPCR / 2
)

// unwrapper 把发生模回绕的短位宽时钟展开为单调递增的 64 位时间。
//
// 输入值只要不连续跳变超过半个时钟周期，就能正确展开；检测到回绕时累加
// 一个完整周期。reset 用于流不连续（adaptation_field 的 discontinuity
// indicator）之后重新建立基准，避免把跳变错误地展开成超大时间。
type unwrapper struct {
	last    uint64 // 最近一次展开结果
	prevRaw uint64 // 最近一次原始时钟值
	mod     uint64
	half    uint64
	started bool
}

func newUnwrapper(mod, half uint64) *unwrapper {
	return &unwrapper{mod: mod, half: half}
}

// push 接收当前的原始（未展开）时钟值，返回展开后的单调时间。
func (u *unwrapper) push(raw uint64) uint64 {
	raw %= u.mod
	if !u.started {
		u.started = true
		u.prevRaw = raw
		u.last = raw
		return raw
	}

	// 在模空间内计算相对上一个原始值的带符号增量，
	// 跳变超过半个周期即视为发生了一次回绕。
	delta := int64(raw) - int64(u.prevRaw)
	if delta > int64(u.half) {
		delta -= int64(u.mod)
	} else if delta < -int64(u.half) {
		delta += int64(u.mod)
	}

	var value uint64
	if delta >= 0 {
		value = u.last + uint64(delta)
	} else if back := uint64(-delta); back < u.last {
		value = u.last - back
	}

	// 保证严格单调：小幅抖动或重复值不允许时间倒退。
	if value < u.last {
		value = u.last
	}
	u.prevRaw = raw
	u.last = value
	return value
}

// reset 在时间轴不连续后清零基准，下一个值将作为新的时间原点。
func (u *unwrapper) reset() {
	u.started = false
	u.last = 0
	u.prevRaw = 0
}

// newPTSUnwrapper 用于 PTS / DTS（33 位，90kHz）。
func newPTSUnwrapper() *unwrapper {
	return newUnwrapper(mod33, half33)
}

// newPCRUnwrapper 用于 PCR（42 位，27MHz）。
func newPCRUnwrapper() *unwrapper {
	return newUnwrapper(modPCR, halfPCR)
}
