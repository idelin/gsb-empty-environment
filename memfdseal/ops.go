package memfdseal

import "fmt"

// Op 是对 memfd 的一次模拟操作。该接口方法不导出，操作集合由本包封闭定义，
// 与题目列举的系统调用一一对应：ftruncate、write、mmap、munmap、fcntl 加封印、
// dup 传递，并额外提供通过既有映射写入的 MapWrite 用来观察 F_SEAL_FUTURE_WRITE
// 的语义缺口。
type Op interface {
	apply(m *Machine) (mappingID int, err error)
}

// TruncateOp 模拟 ftruncate(fd, Size)。
type TruncateOp struct{ Size int64 }

func (op TruncateOp) apply(m *Machine) (int, error) { return 0, m.Truncate(op.Size) }

// WriteOp 模拟 write(fd, Data)，从当前文件偏移写入。
type WriteOp struct{ Data []byte }

func (op WriteOp) apply(m *Machine) (int, error) { return 0, m.Write(op.Data) }

// MmapOp 模拟 mmap(nil, Length, prot, flags, fd, 0)：
// Writable 对应 PROT_WRITE，Shared 对应 MAP_SHARED（否则 MAP_PRIVATE）。
type MmapOp struct {
	Writable bool
	Shared   bool
	Length   int
	// MappingID 在执行成功后由状态机回填，供后续 MunmapOp/MapWriteOp 引用。
	MappingID int
}

func (op MmapOp) apply(m *Machine) (int, error) {
	return m.Mmap(op.Writable, op.Shared, op.Length)
}

// MunmapOp 模拟 munmap，MappingID 取自 MmapOp 的执行结果。
type MunmapOp struct{ MappingID int }

func (op MunmapOp) apply(m *Machine) (int, error) { return 0, m.Munmap(op.MappingID) }

// MapWriteOp 模拟对存活映射的一次内存写。
type MapWriteOp struct{ MappingID int }

func (op MapWriteOp) apply(m *Machine) (int, error) { return 0, m.MapWrite(op.MappingID) }

// AddSealsOp 模拟 fcntl(fd, F_ADD_SEALS, Seals)。
type AddSealsOp struct{ Seals SealSet }

func (op AddSealsOp) apply(m *Machine) (int, error) { return 0, m.AddSeals(op.Seals) }

// DupOp 模拟无门禁的 dup(2)，只计数、不校验交付条件。
type DupOp struct{}

func (op DupOp) apply(m *Machine) (int, error) {
	m.Dup()
	return 0, nil
}

// DeliverOp 模拟把描述符真正移交出去，未满足交付条件时返回 ErrNotDeliverable。
type DeliverOp struct{}

func (op DeliverOp) apply(m *Machine) (int, error) {
	_, err := m.Deliver()
	return 0, err
}

func (op MmapOp) String() string {
	return fmt.Sprintf("mmap(writable=%v shared=%v length=%d)", op.Writable, op.Shared, op.Length)
}

// StepResult 是序列中单个操作的执行记录。
type StepResult struct {
	Index int   // 从 0 开始的操作序号
	Op    Op    // 被执行的操作
	Err   error // 模拟系统调用的失败；nil 表示成功
}

// Report 汇总整条操作序列的执行结果与最终交付判定。
type Report struct {
	Steps          []StepResult
	BlockedReasons []string
	Deliverable    bool
	Seals          SealSet
}

// Run 按顺序对状态机施加整条操作序列。单个操作失败不会中断后续操作，与真实
// 系统调用序列的行为一致；每一步的成败都记录在 Report.Steps 中，最后给出
// 交付门禁判定。
func (m *Machine) Run(ops []Op) *Report {
	report := &Report{Steps: make([]StepResult, 0, len(ops))}
	for i, op := range ops {
		id, err := op.apply(m)
		result := StepResult{Index: i, Op: op, Err: err}
		if mmapOp, ok := op.(MmapOp); ok && err == nil {
			mmapOp.MappingID = id
			ops[i] = mmapOp
			result.Op = mmapOp
		}
		report.Steps = append(report.Steps, result)
	}
	report.BlockedReasons = m.BlockedReasons()
	report.Deliverable = m.Deliverable()
	report.Seals = m.Seals()
	return report
}
