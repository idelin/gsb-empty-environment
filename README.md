# 固件权限锁定位推导

硬件寄存器一旦锁定就不能再改权限，错误顺序可能永久留下可写敏感区。本包供 Go 配置宿主调用，在最多 24 位的有限状态空间中规划最短、始终安全的写入序列。

## 模型

- `Problem` 描述寄存器初态、目标、锁定位、写操作和禁区。
- `Operation` 用 `Mask/Value` 做原子掩码写入；`RequireMask/RequireValue` 是前置条件。
- `LockMask` 中的位只能从 0 变成 1。默认策略下，任一位锁定后，所有非锁定位冻结；可用 `Locks` 指定锁定位与受保护位的对应关系，或用 `SelfLockOnly` 只保留“锁定位不可清零”的语义。
- `Forbidden`、`ForbiddenStates`、`ForbiddenFunc` 描述任何时刻（包括初态和终态）都不允许出现的安全禁区。
- `MaxSteps` 和 `MaxStates` 是硬预算。预算耗尽时 `Status == StatusIncomplete`，并返回 `BestSteps` 安全前缀；只有完整搜索后才会返回 `StatusUnreachable`。

## 使用

```go
problem := netsec.Problem{
    Bits:     3,
    Initial:  0,
    Target:   0b101,
    LockMask: 0b100,
    Ops: []netsec.Operation{
        {Name: "lock", Mask: 0b100, Value: 0b100},
        {Name: "grant", Mask: 0b001, Value: 0b001,
            RequireMask: 0b100, RequireValue: 0},
    },
}

result, err := netsec.Plan(problem)
if err != nil {
    return err
}
if !result.Complete() {
    // StatusIncomplete: raise MaxSteps/MaxStates and retry.
    // StatusUnreachable: no safe sequence exists.
    return fmt.Errorf("%s: %s", result.Status, result.Reason)
}
for _, step := range result.Steps {
    // Program the host with step.Op (index into problem.Ops).
}
```

`Plan` 的 BFS 保证操作数最少；同一层按 `Ops` 顺序稳定选择。`Next`/`ApplyOperation` 可用于执行前模拟单步写入。

## 死路核对

小位域可调用 `Analyze` 或 `FindDeadEnds`。它枚举全部状态，正向求从初态可达的集合，反向求仍能到达目标的集合；两者之差就是可进入但会永久失去目标路径的状态。

```go
analysis, err := netsec.Analyze(problem)
if err != nil {
    // ErrTooLarge: use a smaller register or AnalyzeLimit explicitly.
}
for _, state := range analysis.DeadEnds {
    // Mark the irreversible configuration as unsafe.
}
```

默认枚举上限为 `DefaultAnalysisLimit`（65536 个状态）。
