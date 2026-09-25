package mvcc

// Failpoints 是故障注入点：任一钩子返回非 nil error 时，
// Commit 立即以该错误失败，模拟进程在对应时刻崩溃。
// 已落盘的日志记录保留，重新 Open 时由恢复流程重放。
type Failpoints struct {
	// BeforeAppend 在事务帧写入 OS 之前触发。
	BeforeAppend func() error
	// AfterAppendBeforeSync 在帧已 write、尚未 fsync 时触发。
	AfterAppendBeforeSync func() error
	// AfterSyncBeforeApply 在 fsync 完成、内存索引尚未应用时触发
	// （模拟"已持久化但未来得及应用"的崩溃点）。
	AfterSyncBeforeApply func() error
}
