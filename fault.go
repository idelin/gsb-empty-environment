package diskdeque

import "errors"

// FaultPoint 标识事务提交过程中可注入故障的位置。
type FaultPoint int

const (
	// FaultBeforeJournal：新块 tmp 已落盘、journal 尚未写入。
	// 在此失败等价于“进程在提交点之前退出”，事务可干净回滚。
	FaultBeforeJournal FaultPoint = iota
	// FaultAfterJournal：journal 已 fsync、块尚未 rename。
	// 在此失败等价于“进程崩溃/断电”，重开目录时事务被前滚重放。
	FaultAfterJournal
	// FaultAfterBlockRename：块已 rename、超级块尚未替换。崩溃后前滚。
	FaultAfterBlockRename
	// FaultAfterSuperblock：超级块已原子替换、journal 尚未清理。崩溃后前滚（幂等）。
	FaultAfterSuperblock
)

// ErrInjected 由注入点返回，表示人为制造的故障。
var ErrInjected = errors.New("diskdeque: injected fault")

var faultHooks = map[FaultPoint]func() error{}

// SetFaultHook 设置故障注入点（供测试与故障演练使用）。
// hook 返回非 nil 错误时，提交流程在该点中断。
// 传 nil 可清除注入点。
func SetFaultHook(p FaultPoint, hook func() error) {
	faultHooks[p] = hook
}

// ClearFaultHooks 清除全部故障注入点。
func ClearFaultHooks() {
	for p := range faultHooks {
		delete(faultHooks, p)
	}
}

func faultAt(p FaultPoint) error {
	if h := faultHooks[p]; h != nil {
		return h()
	}
	return nil
}
