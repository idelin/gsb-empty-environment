package volmap

// FaultPoint 标识故障注入点，用于在测试中模拟进程崩溃/断电。
type FaultPoint int

const (
	// FaultAfterBlockSync 物理块已写盘并 fsync，清单尚未写入。
	// 崩溃后果：产生一个不被清单引用的孤儿块，Open 时回收。
	FaultAfterBlockSync FaultPoint = iota
	// FaultBeforeManifestRename 清单临时文件已写盘并 fsync，rename 尚未发生。
	// 崩溃后果：旧清单仍然有效，临时文件在 Open 时丢弃。
	FaultBeforeManifestRename
	// FaultAfterManifestRename rename 已完成，目录尚未 fsync。
	// 崩溃后果：进程崩溃不影响（rename 已生效）；断电时由文件系统
	// 保证 rename 的原子性，状态要么旧要么新，不会撕裂。
	FaultAfterManifestRename
)

// FaultHook 在提交路径的关键点被调用。返回非 nil error 表示本次操作
// 以错误中止（状态回退到上一次已提交状态）；panic 则模拟进程崩溃，
// 调用方不会得到任何清理，重新 Open 后应看到一致状态。
type FaultHook func(FaultPoint) error
