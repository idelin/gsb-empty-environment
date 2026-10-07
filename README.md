# plancmp

Go 计划比较库：输入旧计划已执行的操作前缀与新计划（带依赖的操作集），
在已执行操作不可撤销的前提下，求能继续到达新目标的**最小补充操作集**。

## 模型

- 状态为有限布尔变量集，至多 `MaxVars = 12` 个变量，`State` 即位掩码。
- 每个操作 `Op` 含名称、依赖 `Deps`、前置条件（`Require`/`Forbid`）与
  效果（`Set`/`Clear`）。
- `Input.Executed` 为已执行（不可撤销）的操作；`Input.Initial` 为执行后
  的当前状态；`Input.Committed` 为被初始承诺锁定的变量掩码，这些变量必须
  保持 `Initial` 中的值，任何补充操作都不得悄悄重置它们。

## 用法

```go
res, err := plancmp.Diff(&plancmp.Input{
    Initial:   v0,                // 已执行操作后的状态
    Executed:  []string{"a"},     // 不可撤销
    Committed: v0,                // v0 的值被承诺锁定
    New: &plancmp.Plan{
        Ops: []plancmp.Op{
            {Name: "a", Set: v0},
            {Name: "b", Deps: []string{"a"}, Set: v1},
        },
        GoalRequire: v1,
    },
})
// res.Reachable == true, res.Added == []string{"b"}, res.Verified == true
```

## 语义

- `Diff` 在 `(状态, 已应用操作集)` 上做广度优先搜索，每步恰好补充一个
  操作，因此首个到达目标的序列即最小补充操作集（按操作个数）。
- 依赖可指向新计划内的操作或已执行的操作；已执行操作视为已应用。
- 不可达时返回 `Result{Reachable: false, Explored: ...}`，`Explored`
  为完整搜索范围（搜索中到达的全部状态，升序）。
- 找到序列后，`Diff` 内部调用 `Replay` 重放验证：逐步检查依赖顺序、
  前置条件，并证明初始承诺不会被悄悄重置，通过时置 `Verified`。
- `Replay` 也可独立使用，用于审计任意候选操作序列。

## 测试

```sh
GO111MODULE=on go test ./...
```
