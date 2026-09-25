package diskqueue

// FaultPoint 标识故障注入点，仅用于测试/演练，不注入时零开销。
type FaultPoint string

const (
	// FaultBeforeAppend 在记录写入 WAL 之前触发。
	FaultBeforeAppend FaultPoint = "before_append"
	// FaultAfterAppendBeforeSync 在记录已写入 OS、尚未 fsync 时触发。
	FaultAfterAppendBeforeSync FaultPoint = "after_append_before_sync"
	// FaultAfterSync 在记录已 fsync、尚未应用到内存堆时触发（模拟“已落盘但进程崩溃”）。
	FaultAfterSync FaultPoint = "after_sync"
)

// FaultHook 在注入点被调用；返回非 nil error 表示该次操作在此点失败。
// recType 为 WAL 记录类型（1=Enqueue 2=Update 3=Delete 4=PopCommit）。
type FaultHook func(point FaultPoint, recType byte) error

func (q *Queue) inject(point FaultPoint, recType byte) error {
	if q.hook == nil {
		return nil
	}
	return q.hook(point, recType)
}
