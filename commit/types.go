package commit

// 本包实现“镜像块设备事务提交器”。
//
// 约束摘要：
//   - 前端为每个待写扇区提供一份拼接配方，片段长度按切片顺序拼接，长度之和恰为一个扇区；
//   - 只有 K 个可跨动作驻留的整扇区缓存槽，一次读装入一个“尚未被覆盖”的入口扇区；
//   - 写指令使用专用组装寄存器：写入瞬间，配方引用的所有源必须同时驻留缓存，
//     碎片不能跨指令保存；目标自身是源时，也必须读到其入口旧值；
//   - 覆盖入口扇区不破坏已驻留的缓存副本；
//   - 读费用为非负整数，取决于源扇区柱面与当前磁头所在柱面；
//   - 任何读、写都把磁头移到其扇区所在柱面，释放不移动磁头，也不产生费用；
//   - 目标：严格最小化总读费用，同费用下最小化读次数。
//
// 规划过程只消费 Instance 描述，不接触物理盘；Commit 才会驱动 Device。

// SectorID 是稀疏地址空间中的扇区地址（无需连续）。
type SectorID uint64

// Cylinder 是磁头柱面号。
type Cylinder int64

// Fragment 是目标扇区配方中的一个片段。
//
// 若 Source 为 nil，片段由 Constant 中的常量字节组成；否则引用入口扇区 Source
// 中字节区间 [SourceOffset, SourceOffset+Length)。
type Fragment struct {
	Source       *SectorID
	SourceOffset int
	Length       int
	Constant     []byte
}

// Target 描述一个待写扇区及其拼接配方。
type Target struct {
	Address   SectorID
	Sector    Cylinder
	Fragments []Fragment
}

// ReadCostFunc 返回磁头位于 head 柱面、读取位于 cyl 柱面的扇区的费用。
// 费用必须是非负整数，调用方负责保证不截断地累加。
type ReadCostFunc func(head, cyl Cylinder) int64

// Instance 是一次事务的完整静态描述。规划期间只读它，不读盘面。
type Instance struct {
	SectorSize int
	K          int
	Initial    Cylinder
	Cylinders  map[SectorID]Cylinder
	ReadCost   ReadCostFunc
	Targets    []Target
}

// Op 是可直接驱动物理接口的单个动作。Kind 取 "read"、"release"、"write"。
type Op struct {
	Kind    string
	Sector  SectorID
	Cyl     Cylinder
	Payload []byte // 仅 write 携带组装好的完整扇区
}

// Plan 是一份完整提交计划：动作序列可直接交给物理设备执行。
type Plan struct {
	Ops      []Op
	ReadCost int64
	Reads    int
}

// InfeasibleError 表示在给定槽位与互引约束下不存在可行提交序列。
type InfeasibleError struct {
	Reason string
}

func (e *InfeasibleError) Error() string {
	return "commit: no feasible sequence: " + e.Reason
}

func infeasible(reason string) error { return &InfeasibleError{Reason: reason} }
