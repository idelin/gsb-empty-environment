# 材质扫描的多盏灯存在未知曝光偏差，逐像素拟合法线会得到彼此矛盾的灯强

材质扫描的多盏灯存在未知曝光偏差，逐像素拟合法线会得到彼此矛盾的灯强。给Go资产重建宿主实现联合校准包，至多8盏灯、每灯最多3个正有理曝光候选，首灯曝光固定为1；所有像素必须共用同一曝光向量。每像素亮度模型为max(0,曝光乘已知有理光向量点乘g)，g为反照率乘法线；对每个曝光向量枚举满秩三灯组解出候选g，以给定有理残差阈值选一致观测最多、再平方误差最小的候选。全图同样先最大化一致数再最小化误差，并列按曝光索引及三灯ID决胜；最多32像素，饱和观测显式排除，预算不足返回当前可行解及未最优标记。输出法线、反照率与拒绝观测，使用精确有理比较和小实例全组合参考验证，不能分别选各像素最有利的曝光。

## 包布局（`package taskpkg`）

- `types.go`：`Problem` / `Result` / `Observation` / `PixelResult` /
  `RejectedObservation` 等输入输出类型与规模常量。
- `errors.go`：`ErrInvalidProblem`、`ErrBudgetExhausted`、
  `ErrNoFeasibleSolution`。
- `rational.go`：基于 `math/big` 的精确有理工具（点积、行列式、克拉默
  求解、绝对值/平方等），所有比较均为精确有理比较。
- `exposure.go`：输入校验与曝光向量字典序枚举器（首灯恒为 1）。
- `solver.go`：`Solve` 主流程——逐像素枚举满秩三灯组、像素级与全图
  两级（一致数优先、平方误差次之、ID 决胜）选择、饱和排除与预算控制。
- `reference.go`：结构独立的小实例全组合参考实现，仅供测试交叉验证。

## 用法

```go
res, err := taskpkg.Solve(problem)
if errors.Is(err, taskpkg.ErrBudgetExhausted) {
    // res 仍为当前最优可行解，res.NotOptimal == true
}
```

- `problem.Budget == 0` 表示不设预算；正数为满秩三灯组评估上界。
- 饱和观测（`Observation.Saturated`）在任何拟合前显式排除，并出现在
  `Result.Rejected`（原因 `saturated`）；残差超阈的非饱和观测原因是
  `residual`。
- 法线为单位向量（`PixelResult.Normal`），反照率为 `‖g‖`
  （`PixelResult.Albedo`），精确有理的 `g` 同时保留在 `PixelResult.G`。

## 验证

`go test ./...` 覆盖：合成真值恢复、与独立全组合参考的对拍、饱和排除、
全图共享曝光、预算边界（恰好/不足/极小）、秩亏不可解、输入校验与输入
不可变性。
