# 覆盖反馈稀有度更新

安全模糊测试在语料淘汰后仍沿用旧覆盖频次，会长期饿死真正稀有的路径。写供Go测试宿主调用的能量分配包，样本覆盖集合及有效执行次数可增删，每轮能量按最稀有覆盖频率倒数分配并用最大余数法取整数；总预算守恒，平局按样本ID。用全量重算对照并发合并、撤销失败样本与无新覆盖样本。

## energy 包

实现位于 `energy/`（导入路径 `example.org/netsec/q0406/energy`），供模糊测试宿主在每轮调度前调用。

调度模型：

- 每个样本记录覆盖特征集合（自动去重）与有效执行次数；特征频次 = 当前所有覆盖该特征样本的有效执行次数之和。删除样本、替换覆盖或修改执行次数时频次立即更新，语料淘汰后不会残留旧频次。
- 样本原始权重取其覆盖特征中最小频次（最稀有覆盖）的倒数 `1/rarest`；无覆盖或有效执行次数为 0 的样本权重为 0，分不到能量（无新覆盖样本）。
- 整数化使用最大余数法（Hare quota），全程 `math/big` 有理运算，无浮点误差；余数相同按样本 ID 字典序决胜。只要存在正权重样本，分配能量之和严格等于预算，否则预算整体不支出。
- `Allocator` 内部用 `sync.RWMutex` 保护，可并发调用；`Tx`/`Merge` 在私有状态副本上批量校验并应用，任一步失败（如撤销不存在的失败样本）整体回滚，活态状态不变。
- `RecomputeAllocate` 是纯函数式全量重算 oracle，测试中用于与增量维护的 `Allocator.Allocate` 做差分对照。

最小用法：

```go
allocator := energy.New()
allocator.Add(energy.Sample{ID: "s0", Coverage: []energy.Feature{1, 2}, Execs: 3})
allocator.Add(energy.Sample{ID: "s1", Coverage: []energy.Feature{2}, Execs: 1})

// 每轮分配，结果按样本 ID 排序，能量之和守恒。
allocations, err := allocator.Allocate(100)

// 语料淘汰：频次立即扣除，稀有路径不再被饿死。
allocator.Remove("s0")

// 批量合并，失败样本导致整批原子回滚。
err = allocator.Merge([]energy.Op{
    energy.OpAddOp(energy.Sample{ID: "s2", Coverage: []energy.Feature{3}, Execs: 1}),
    energy.OpRemoveOp("crashed-sample"),
})

// 全量重算对照。
oracle, err := energy.RecomputeAllocate(allocator.Samples(), 100)
```

验证：`go test ./...`（含 3000 轮随机差分、并发合并、回滚与无覆盖用例），`go test -race ./...` 通过。
