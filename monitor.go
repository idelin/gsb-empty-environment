package vecpool

import "runtime"

// MemSample 是一次堆内存与 GC 状态的采样。
type MemSample struct {
	Mallocs   uint64 // 累计堆分配对象数
	Frees     uint64 // 累计释放对象数
	HeapAlloc uint64 // 当前堆上存活字节数
	NumGC     uint32 // 累计 GC 次数
	PauseNs   uint64 // 累计 GC 停顿纳秒
}

// SampleHeap 采样当前进程的内存与 GC 指标。
//
// 注意：Go 运行时在进程内前几次调用 ReadMemStats 时会进行
// 一次性内部初始化（自身产生极少量堆分配）。因此监控器在
// 正式建立基线前必须先调用 Warmup 预热探针，否则会把探针
// 自身的初始化开销误报为被测流水线的分配。
func SampleHeap() MemSample {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	var pause uint64
	for _, p := range m.PauseNs[:] {
		pause += p
	}
	return MemSample{
		Mallocs:   m.Mallocs,
		Frees:     m.Frees,
		HeapAlloc: m.HeapAlloc,
		NumGC:     m.NumGC,
		PauseNs:   pause,
	}
}

// Warmup 预热内存监控探针，耗尽运行时的一次性初始化分配，
// 使后续采样自身达到零分配。任何基于 SampleHeap 差值的
// 测量都应在建立基线前调用本函数。
func Warmup() {
	for i := 0; i < 16; i++ {
		SampleHeap()
	}
}

// Delta 返回两次采样之间的差值（监控报告用）。
type Delta struct {
	Mallocs   uint64
	HeapAlloc int64
	NumGC     int32
	PauseNs   int64
}

// Diff 计算 end - start。
func Diff(start, end MemSample) Delta {
	return Delta{
		Mallocs:   end.Mallocs - start.Mallocs,
		HeapAlloc: int64(end.HeapAlloc) - int64(start.HeapAlloc),
		NumGC:     int32(end.NumGC) - int32(start.NumGC),
		PauseNs:   int64(end.PauseNs) - int64(start.PauseNs),
	}
}
