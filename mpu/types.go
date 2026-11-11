// Package mpu 为固件管理层提供 ARMv7-M 风格 MPU 的配置编译与安全安装能力。
//
// 该包对硬件做如下抽象（与 Cortex-M3/M4 的 PMSA-v7 MPU 一致）：
//   - 区域尺寸必须是 2 的幂（最小 32 字节），基址按区域尺寸对齐；
//   - 每个区域有 8 个等长子区，SRD 中置位的子区被屏蔽，最小保护粒度为 4 字节；
//   - 区域按槽位号定优先级，编号越大优先级越高，高优先级区域覆盖低优先级；
//   - 使能且权限为 PermNone 的区域表示“显式拒绝”，匹配时会压制所有低优先级区域。
package mpu

import "errors"

// Perm 是读/写/执行权限的位组合。PermNone 表示显式拒绝所有访问。
type Perm uint8

const (
	PermNone Perm = 0
	PermR    Perm = 1 << iota
	PermW
	PermX
)

// Contains 报告 p 是否包含 q 的全部权限位。
func (p Perm) Contains(q Perm) bool { return p&q == q }

func (p Perm) String() string {
	if p == PermNone {
		return "---"
	}
	b := make([]byte, 0, 3)
	if p.Contains(PermR) {
		b = append(b, 'R')
	}
	if p.Contains(PermW) {
		b = append(b, 'W')
	}
	if p.Contains(PermX) {
		b = append(b, 'X')
	}
	return string(b)
}

// Interval 描述半开区间 [Start, End) 上的期望权限。
// Priority 越大优先级越高；优先级相同的重叠区间会被判定为歧义而拒绝。
type Interval struct {
	Name     string
	Start    uint64
	End      uint64
	Perm     Perm
	Priority int
}

// Region 是可直接写入 MPU 区域寄存器的描述符。
// 区域尺寸为 1<<SizeLog2，SRD 的第 i 位置位表示屏蔽第 i 个子区。
type Region struct {
	Slot     int
	Base     uint64
	SizeLog2 uint8
	SRD      uint8
	Perm     Perm
}

// MPU 几何约束常量。
const (
	MinRegionSizeLog2 = 5  // 最小区域 32 字节
	MaxRegionSizeLog2 = 32 // 最大区域 4 GiB
	SubregionCount    = 8  // 每个区域 8 个子区
	// MinSubregionBytes 是 MPU 能表达的最小保护粒度：32B 区域 / 8 子区。
	MinSubregionBytes = 1 << MinRegionSizeLog2 / SubregionCount
)

// 哨兵错误，调用方可用 errors.Is 判定失败类别。
var (
	// ErrNotRepresentable：期望映射无法由有限数量的对齐区域与子区屏蔽表达。
	ErrNotRepresentable = errors.New("mpu: memory map cannot be represented by aligned regions and subregion masks")
	// ErrUnsafeTransition：不存在能保证切换期间不短暂扩大 W/X 范围的寄存器写序列。
	ErrUnsafeTransition = errors.New("mpu: no register write sequence can avoid transient permission widening")
	// ErrInvalidRegion：区域描述符本身违反 MPU 几何约束。
	ErrInvalidRegion = errors.New("mpu: invalid region descriptor")
)

// MapErrorKind 区分不可表达的具体原因。
type MapErrorKind int

const (
	MapAlignment  MapErrorKind = iota + 1 // 边界达不到 4 字节子区粒度
	MapCapacity                           // 所需区域数超过硬件区域数量
	MapAmbiguous                          // 同优先级区间重叠，期望权限不确定
	MapOutOfRange                         // 区间越出地址空间
)

// MapError 携带不可表达（或非法）映射的现场信息，便于固件层上报。
type MapError struct {
	Kind   MapErrorKind
	Addr   uint64 // 触发问题的代表性地址（对齐错误时为区间起点）
	Detail string
}

func (e *MapError) Error() string {
	name := map[MapErrorKind]string{
		MapAlignment:  "alignment",
		MapCapacity:   "region capacity",
		MapAmbiguous:  "ambiguous priority",
		MapOutOfRange: "out of range",
	}[e.Kind]
	return "mpu: " + name + " error at 0x" + hex(e.Addr) + ": " + e.Detail
}

// Unwrap 让 errors.Is 能匹配到对应哨兵错误。
func (e *MapError) Unwrap() error {
	switch e.Kind {
	case MapAlignment, MapCapacity:
		return ErrNotRepresentable
	default:
		return ErrInvalidRegion
	}
}

// TransitionError 说明安全切换失败的阶段、槽位与见证地址。
type TransitionError struct {
	Phase  string
	Slot   int
	Addr   uint64
	Detail string
}

func (e *TransitionError) Error() string {
	return "mpu: unsafe transition in phase " + e.Phase +
		" at slot " + itoa(e.Slot) + " addr 0x" + hex(e.Addr) + ": " + e.Detail
}

// Unwrap 返回 ErrUnsafeTransition。
func (e *TransitionError) Unwrap() error { return ErrUnsafeTransition }

func hex(v uint64) string {
	const digits = "0123456789abcdef"
	if v == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = digits[v&0xf]
		v >>= 4
	}
	return string(b[i:])
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
