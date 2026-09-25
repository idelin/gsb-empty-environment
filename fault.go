package diskbtree

import "errors"

// ErrSimulatedCrash 是故障注入触发时 panic 的值, 模拟进程崩溃/断电。
// 触发后该 DB 句柄必须丢弃, 通过重新 Open 同一目录来模拟重启恢复。
var ErrSimulatedCrash = errors.New("diskbtree: simulated crash")

// FaultPoint 标识提交协议中的故障注入点。
type FaultPoint int

const (
	// FaultBeforeDataSync 新页已写入数据文件, 尚未 fsync。
	FaultBeforeDataSync FaultPoint = iota
	// FaultAfterDataSync 数据文件已 fsync, 新 superblock 尚未写入。
	// 此点崩溃等价于"叶已写好但父节点未发布": 恢复后只能看到旧状态。
	FaultAfterDataSync
	// FaultAfterMetaWrite 新 superblock 已写入, 尚未 fsync。
	// 此点崩溃后恢复到旧或新状态均合法, 取决于操作系统是否落盘。
	FaultAfterMetaWrite
)

func (f FaultPoint) String() string {
	switch f {
	case FaultBeforeDataSync:
		return "BeforeDataSync"
	case FaultAfterDataSync:
		return "AfterDataSync"
	case FaultAfterMetaWrite:
		return "AfterMetaWrite"
	}
	return "Unknown"
}

// fire 在注入点询问钩子是否"崩溃"。
func (db *DB) fire(fp FaultPoint) {
	if db.hook != nil && db.hook(fp) {
		panic(ErrSimulatedCrash)
	}
}
