# permverifier

内存宿主权限验收器（Go，可导入包 `prescreen.local/ddd-1581-20261122-v2/permverifier`）。

## 模型

- 映射以“对象代次 + 半开偏移区间”描述，支持共享帧、写时复制（COW）、分段重映射与任意别名。
- 视图携带 `R/W/X` 权限位和 CPU 列表；同一物理字节在任意有效视图上同时具备 W 与 X 即冲突，即使单个视图自身只 W 或只 X。
- `RevokeOp` 进入撤销中状态，旧权限在每个持有 CPU 的 `AckOp(CPU, 对象, 代次)` 到齐前不释放；回执丢失即旧权限保留。
- `ForkOp` 共享父 COW 页表；`WriteOp` 仅对真正相交的页分配私有帧（页粒度），未触页继续共享。
- 批次只有依赖边、没有全局顺序；`Verify` 对 DAG 允许的全部线性交错做 BFS 穷举。

## 结论

- `VerdictSafe`：所有可达交错均满足 W^X。
- `VerdictUnsafe`：返回最短失败前缀 `Prefix` 与首个物理冲突区间 `Conflict`（含写入/执行视图引用）。
- `VerdictIncomplete`：无冲突但存在永远不可使能的操作，或终态仍有未确认撤销。
- `VerdictUnknown`：`Options.MaxStates/MaxSteps` 预算不足，不下不安全结论。

## 用法

```go
rep, err := permverifier.Verify(batch, permverifier.Options{
    MaxStates: 100000,
    MaxSteps:  200,
    Pool:      permverifier.Interval{Lo: 0x8000_0000_0000, Len: 1 << 30},
})
if err != nil { /* 批次静态非法 */ }
switch rep.Verdict {
case permverifier.VerdictUnsafe:
    // rep.Prefix 最短失败前缀；rep.Conflict.Phys 首个冲突物理区间
case permverifier.VerdictUnknown:
    // 预算不足
}
```

`SmallPageTable` 是独立的逐字节参考模型：按状态迁移逐个 `MapView/UnmapView`，
用 `Conflict()` 与符号引擎报告的区间交叉核对（见 `*_test.go`）。

## 64 位语义

所有区间为 `uint64` 半开区间，允许排他末端恰为 `2^64`；会绕过 `2^64` 的构造在校验阶段报错，不发生回绕。
