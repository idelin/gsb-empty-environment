package deque

// FaultPoint 标识提交路径上的故障注入点。
// 通过在 Options.Fail 中返回错误,可以模拟进程在
// 该点崩溃(掉电语义见 README 与 Commit 注释)。
type FaultPoint int

const (
	// FaultBeforeBlockWrite 在写任何脏块文件之前。
	FaultBeforeBlockWrite FaultPoint = iota
	// FaultAfterBlockSync 在所有脏块写完并 fsync 之后、写 meta 临时文件之前。
	FaultAfterBlockSync
	// FaultBeforeMetaRename 在 meta 临时文件写完并 fsync 之后、rename 之前。
	// 这是提交点之前最后一个注入点:在此崩溃,提交整体不可见。
	FaultBeforeMetaRename
	// FaultAfterMetaRename 在 rename 之后、目录 fsync 之前。
	// 提交点已过:恢复后新状态可见(断电时目录项可能丢失,
	// 则回退到旧状态,两种结果都一致)。
	FaultAfterMetaRename
)

func (fp FaultPoint) String() string {
	switch fp {
	case FaultBeforeBlockWrite:
		return "BeforeBlockWrite"
	case FaultAfterBlockSync:
		return "AfterBlockSync"
	case FaultBeforeMetaRename:
		return "BeforeMetaRename"
	case FaultAfterMetaRename:
		return "AfterMetaRename"
	}
	return "Unknown"
}

func (d *Deque) inject(fp FaultPoint) error {
	if d.fail != nil {
		return d.fail(fp)
	}
	return nil
}
