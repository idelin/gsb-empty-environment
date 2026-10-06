// Package testselect 实现基于函数版本与调用图的回归测试选择。
//
// 基线快照记录：每个被测函数的版本哈希、静态/动态调用边、
// 以及每个测试覆盖到的函数集合。下一次选择时与当前代码状态对比：
//   - 函数被修改：选中覆盖它的测试，并沿调用图反向传播给调用方；
//   - 调用边改变：在旧图与新图的并集上传播，增删边都会波及边的调用方；
//   - 新增函数覆盖未知：无法确定影响面，触发全量运行标记；
//   - 删除函数 / 动态边 / 覆盖过期：用测试覆盖集的全依赖闭包核对。
package testselect

// Snapshot 是上一次测试运行后持久化的基线状态。
type Snapshot struct {
	// Functions 记录函数 ID -> 版本哈希（内容指纹）。
	Functions map[string]string
	// CallGraph 记录静态调用边：caller -> callees。
	CallGraph map[string][]string
	// Dynamic 记录运行期观测到的动态分派边（接口、反射等）：caller -> callees。
	Dynamic map[string][]string
	// Coverage 记录测试 ID -> 该测试覆盖到的函数 ID 列表。
	Coverage map[string][]string
}

// Current 描述当前代码的函数版本与调用图。
type Current struct {
	Functions map[string]string
	CallGraph map[string][]string
	Dynamic   map[string][]string
}

// Selection 是一次测试选择的结果。
type Selection struct {
	// RunAll 为 true 时表示无法精确选择，必须全量运行。
	RunAll bool
	// Reason 说明触发全量运行的原因。
	Reason string
	// Tests 为需要运行的测试 ID，已排序。
	Tests []string
	// Reasons 记录每个测试被选中的原因。
	Reasons map[string][]string
	// Stale 为覆盖已过期的测试（覆盖集引用了已删除的函数）。
	Stale []string
}

// NewSnapshot 返回一个空的基线快照。
func NewSnapshot() *Snapshot {
	return &Snapshot{
		Functions: make(map[string]string),
		CallGraph: make(map[string][]string),
		Dynamic:   make(map[string][]string),
		Coverage:  make(map[string][]string),
	}
}
