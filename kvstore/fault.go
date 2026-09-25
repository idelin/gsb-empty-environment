package kvstore

// Faults 是故障注入点集合，用于在测试中模拟进程崩溃/断电。
// 钩子通常以 panic 方式注入（测试 recover 后调用 simulateCrash 并重新 Open），
// 等价于进程在该时刻被杀掉：内存状态丢失，目录里只留下已落盘的字节。
// Store 持有 *Faults 指针，测试可在 Open 之后随时安装/拆除钩子。
type Faults struct {
	// AfterRecordAppend 在记录字节写入活跃段之后、（可选的）fsync 与索引更新之前触发。
	AfterRecordAppend func()
	// BeforeManifestRename 在 MANIFEST.tmp 写完并 fsync 之后、原子 rename 之前触发。
	// 此处崩溃：旧 MANIFEST 仍然有效，新段成为孤儿文件，重启时被清理。
	BeforeManifestRename func()
	// AfterManifestRename 在 rename 完成之后、目录 fsync 与内存切换之前触发。
	// 此处崩溃：新 MANIFEST 已生效，旧段成为孤儿文件，重启时被清理。
	AfterManifestRename func()
}

// fire 在钩子存在时调用它；f 为 nil（未配置注入）时安全跳过。
func (s *Store) fire(selectHook func(*Faults) func()) {
	if s.faults == nil {
		return
	}
	if hook := selectHook(s.faults); hook != nil {
		hook()
	}
}
