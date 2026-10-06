# plancmp

Go 计划比较库：输入旧、新两组带依赖关系的操作，在已执行操作不可撤销的
前提下，求能继续到达新目标的最小补充操作集。

## 模型

- 世界状态为至多 12 个布尔变量（`State` 位掩码），搜索空间有限、可穷举。
- 每个操作 `Op` 含依赖（`Deps`）、前置条件（`Pre`）与效果（`Eff`）。
- 已执行操作的效果构成"承诺"（commitment）：补充操作若会把已承诺变量
  重置为相反值，则被排除在搜索之外。

## 用法

```go
res, err := plancmp.Reconcile(oldPlan, newPlan, initial, executed, goal)
```

- `Reachable == true`：`Steps` 为按执行顺序排列的最小补充操作集（BFS 保证
  步数最少）；返回前已对结果做完整重放验证。
- `Reachable == false`：`Explored` 返回完整搜索范围（每个可达状态及到达它
  所用的操作集合），用于诊断为何不可达。

`Plan.Verify(initial, executed, steps, goal)` 可独立审计任意操作序列：
重放已执行前缀与补充步骤，校验依赖、前置条件，并保证初始承诺不会被
悄悄重置，最终确认目标达成。

## 测试

```sh
go test ./...
```
