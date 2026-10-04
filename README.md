# segsolver

连续分段求解器（Go 实现，兼容 Go 1.11+）。

## 问题模型

长度为 `n` 的序列有内部切点 `1..n-1`。一种合法切分必须满足：

- 每段长度在 `[minLen, maxLen]` 内；
- 每个**必须切**位置都被切开；
- 每个**禁止切**位置都不被切开；
- 必须切与禁止切冲突时，`New` 返回列出全部冲突位置的错误。

段成本由整数表 `cost` 给出：`cost[l]` 是长度为 `l` 的一段的费用，**允许为负**。

## 求解语义

- `Solve` 返回最低费用切法；同价时返回切点升序序列**字典序最小**
  （短序列是长序列的前缀时更短者优先）的方案。
- `SolveWithBudget(budget)` 在预算不足时仍返回最便宜的合法切法，
  作为合法上界证据（`WithinBudget=false`）。
- `Check(cuts)` 校验切点证据并重算总费用。
- `Revise(updates)` 支持局部成本修订（按段长更新若干表项），
  下一次 `Solve` 惰性重算。

## 实现

前向 DP 求前缀最优、后向 DP 求后缀最优，复杂度
`O(n·(maxLen-minLen+1))`；字典序最小切点由后缀最优表贪心重建
（能停则停，否则取可行最小切点），避免在 DP 状态中保存切点序列。

## 示例

```go
cost := []int64{0, 2, 5}          // 长度 1 费用 2，长度 2 费用 5
s, _ := segsolver.New(3, 1, 2, cost, nil, nil)
r := s.SolveWithBudget(5)         // r.Feasible, r.Value=6, r.Cuts=[1 2], r.WithinBudget=false
v, err := s.Check(r.Cuts)         // v=6, err=nil —— 证据可复算
s.Revise(map[int]int64{1: -3})    // 局部修订长度 1 的成本
r2 := s.Solve()                   // 按新表重算
```

## 测试

`segsolver_test.go` 对小序列（n ≤ 9）枚举**所有**切分做独立复算，
覆盖冲突位置、负段成本、字典序平局、预算上界与证据校验：

```sh
go test ./...
```
