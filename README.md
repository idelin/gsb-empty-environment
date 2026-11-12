# 片上资源清零覆盖证明

设备复位前需要证明所有可能保存租户数据的寄存器都被清理。实现供Go设备宿主调用的覆盖规划包，寄存器按位描述秘密状态，复位动作清除给定掩码且可能使其他位重新变为未知；最多二十动作，求使秘密位全部确定清零的最短合法序列。返回每步位状态，用全状态搜索核对动作前置条件与不可清零情况。

## 抽象模型

- 每个秘密位只有两种抽象状态：`BitUnknown`（可能保留租户数据）与 `BitZero`（确定清零）。
- 状态打包为 `Mask`（`uint64`）：置位表示 `BitZero`，清零表示 `BitUnknown`，最多规划 64 个秘密位。
- 复位动作 `Action` 含三个掩码：
  - `RequireZero`：前置条件，掩码内所有位必须已是 `BitZero` 动作才合法；
  - `Clear`：动作执行后确定清零的位；
  - `Taint`：动作执行后重新变为未知的位；掩码重叠时 `Taint` 最终生效。

## 规划与证明

- `PlanReset` 从“全部未知”的保守初态出发，`PlanResetFrom` 接受显式逐位初态。
- 规划器在有限抽象状态空间上做广度优先全状态搜索（`MaxActions = 20`）：
  - BFS 按深度扩展，首达全零目标即最短合法序列；
  - 每条迁移都用 `Action.Legal` 核对前置条件；
  - 队列耗尽仍未达目标返回 `ErrUnclearable`，证明不存在合法清零序列；
  - 最短路径超过 20 步返回 `ErrLimitExceeded`。
- 返回的 `Plan` 记录每一步动作及执行前后的掩码与逐位状态（`Step.BeforeBits`/`AfterBits`）。
- `Plan.Verify` 在宿主侧重放整条序列，独立复核前置条件、状态迁移与最终全零。

## 用法

```go
actions := []netsec.Action{
    {Name: "keyzero", Clear: 1 << 0},
    {Name: "unlock", RequireZero: 1 << 0, Clear: 1 << 1},
}

plan, err := netsec.PlanReset(actions)
if errors.Is(err, netsec.ErrUnclearable) {
    // 已穷举可达状态，无法清零
} else if errors.Is(err, netsec.ErrLimitExceeded) {
    // 最短序列超过 20 步
} else if err != nil {
    log.Fatal(err)
}

for _, step := range plan.Steps {
    fmt.Printf("%s: %v -> %v\n", step.Action.Name, step.BeforeBits, step.AfterBits)
}
if err := plan.Verify(actions, 0); err != nil {
    log.Fatalf("reset proof rejected: %v", err)
}
```
