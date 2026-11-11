package mpu

// SlotState 是单个 MPU 区域寄存器组在某一时刻的内容。
// 区域被禁用时 Enabled 为 false，其余字段保留最后一次写入的值。
type SlotState struct {
	Enabled bool
	Region  Region
}

// StepKind 标识一次寄存器写入在安全时序中的阶段语义。
type StepKind string

const (
	// StepDisable：RASR.Enable 清零，关闭区域。
	StepDisable StepKind = "disable-rasr"
	// StepTighten：区域保持使能，RASR 权限改写为 PermNone（显式拒绝）。
	StepTighten StepKind = "tighten-rasr-none"
	// StepWriteRBAR：区域禁用状态下改写 RBAR（基址 + 尺寸）。
	StepWriteRBAR StepKind = "write-rbar"
	// StepEnableNone：写入 RASR（SRD + 使能），权限为 PermNone。
	StepEnableNone StepKind = "enable-rasr-none"
	// StepRaiseAccess：区域保持使能，RASR 权限提升到目标值。
	StepRaiseAccess StepKind = "raise-rasr-perm"
)

// Step 是一次寄存器写入及其前后槽位快照。
// 完整 Plan 中每一步的有效权限都可由这些快照独立复算。
type Step struct {
	Kind   StepKind
	Slot   int
	Before SlotState
	After  SlotState
}

// Plan 是从当前设备状态到目标区域集合的完整寄存器写序列。
type Plan struct {
	SpaceSize uint64
	Initial   []SlotState
	Target    []SlotState
	Steps     []Step
}

// Device 是一个可逐步执行写入的模拟 MPU。
type Device struct {
	spaceSize uint64
	slots     []SlotState
}

// NewDevice 构造模拟 MPU 并装载初始区域。dregion 是硬件区域寄存器数量。
func NewDevice(spaceSize uint64, dregion int, regions []Region) (*Device, error) {
	if dregion <= 0 {
		return nil, &MapError{Kind: MapCapacity, Detail: "no MPU regions available"}
	}
	if len(regions) > dregion {
		return nil, &MapError{Kind: MapCapacity, Detail: "initial region count exceeds DREGION"}
	}
	d := &Device{
		spaceSize: spaceSize,
		slots:     make([]SlotState, dregion),
	}
	for _, r := range regions {
		if err := validateRegion(r, spaceSize); err != nil {
			return nil, err
		}
		if r.Slot < 0 || r.Slot >= dregion {
			return nil, &MapError{Kind: MapOutOfRange, Addr: r.Base,
				Detail: "region slot number out of range"}
		}
		d.slots[r.Slot] = SlotState{Enabled: true, Region: r}
	}
	return d, nil
}

// DRegion 返回硬件区域数量。
func (d *Device) DRegion() int { return len(d.slots) }

// SpaceSize 返回地址空间字节数。
func (d *Device) SpaceSize() uint64 { return d.spaceSize }

// Effective 按槽位号从高到低解释最终权限：
// 第一个使能且覆盖该地址的区域决定权限；无区域命中时为 PermNone。
func (d *Device) Effective(addr uint64) Perm {
	for slot := len(d.slots) - 1; slot >= 0; slot-- {
		s := d.slots[slot]
		if s.Enabled && s.Region.covers(addr) {
			return s.Region.Perm
		}
	}
	return PermNone
}

func (d *Device) snapshot() []SlotState {
	out := make([]SlotState, len(d.slots))
	copy(out, d.slots)
	return out
}

// effectiveSet 穷举整个微型地址空间，返回带有指定权限位的地址集合。
func (d *Device) effectiveSet(bit Perm) bitSet {
	set := newBitSet(int(d.spaceSize))
	for a := 0; a < int(d.spaceSize); a++ {
		if d.Effective(uint64(a)).Contains(bit) {
			set.set(a)
		}
	}
	return set
}

// clone 复制设备，供规划器在穷举分支上自由改写。
func (d *Device) clone() *Device {
	return &Device{spaceSize: d.spaceSize, slots: d.snapshot()}
}

// replay 从初始快照构造设备，用于独立验证 Plan。
func replay(spaceSize uint64, initial []SlotState) *Device {
	slots := make([]SlotState, len(initial))
	copy(slots, initial)
	return &Device{spaceSize: spaceSize, slots: slots}
}
