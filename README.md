# ratls — 有理数精确最小二乘求解器

在 Q 上精确求解超定最小二乘 `min_x ||A x - b||_2`，全程使用
`math/big.Rat`，不经过任何浮点中转。

## 用法

```go
A, _ := ratls.ParseMatrix(3, 2, "1","1", "1","2", "1","3")
b, _ := ratls.ParseVector("1", "2", "2")
sol, err := ratls.LeastSquares(A, b, 0) // 0 = 位长预算不限
if err != nil { ... }
if err := ratls.Verify(A, b, sol, 0); err != nil { ... }
```

`Solution` 给出最小解的完整仿射表示：

- `Particular`：一个特解（自由变量取 0）
- `NullBasis`：`null(A)` 的基；全部最小解 = `Particular + span(NullBasis)`
- `MinNorm`：仿射族中唯一的最小欧氏范数解（对零空间基做精确
  Gram 投影 `(NᵀN)t = -Nᵀx_p` 求得）
- `Rank`：A 的列秩

## 精确性与独立验证

- 正规方程 `(AᵀA)x = Aᵀb`、零空间、RREF 全部在 Q 上精确计算。
- `LeastSquares` 内部自检；`Verify` 从头独立重算 `AᵀA`、`Aᵀb`
  与 `rank(A)`，检查：特解与 MinNorm 满足正规方程、每个零空间基
  向量满足 `Av = 0`（比 `AᵀAv = 0` 更强）、`rank + nullity = n`、
  MinNorm 与零空间正交。

## 位长预算

`budgetBits` 限制计算中任何系数（分子/分母取大者）的位长，
超限返回 `*BudgetError`；`0` 表示不限。

## 测试

```sh
GOCACHE=/tmp/gocache go test ./...
```

覆盖：满秩超定、重复列、全零列、无样本（0×n）、大分母精确恢复、
预算超限报错、仿射族正规方程恒等与 MinNorm 最优性、篡改解被
`Verify` 拒绝、维度不匹配。
