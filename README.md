# simplex — 纯有理数单纯形法求解器（Go 标准库）

精确（无浮点损失）的线性规划单纯形法求解器。所有运算基于 `math/big.Rat`，
迭代过程中的每一次基变量置换（pivot）均为完全有理数运算。

求解标准型：

```
maximize    c^T x
subject to  A x <= b
            x >= 0
```

## 特性

- **纯有理数运算**：全部算术使用 `math/big.Rat`，零精度损失，仅依赖 Go 标准库。
- **两阶段法**：当某些 `b_i < 0` 时自动进入 Phase 1 寻找可行基。
- **Bland 规则**：保证在退化/循环问题上终止（含 Beale 循环用例测试）。
- **精确输出**：
  - `Optimal`：全局精确最优有理数解向量 `X`、最优值 `Objective`、对偶变量（影子价格）`Dual`；
  - `Unbounded`：无界射线证书 `Ray`（`d >= 0`、`A d <= 0`、`c^T d > 0`）；
  - `Infeasible`：Farkas 不可行证书 `Farkas`（`y >= 0`、`A^T y >= 0`、`b^T y < 0`）。
- **完整主元历史**：`Result.Pivots` 记录每次基变量置换的精确有理数主元。

## 用法

```go
c := []*big.Rat{mustRat("2"), mustRat("1")}
A := [][]*big.Rat{
    {mustRat("1"), mustRat("1")},
    {mustRat("1"), mustRat("0")},
    {mustRat("0"), mustRat("1")},
}
b := []*big.Rat{mustRat("3"), mustRat("5/4"), mustRat("2")}

res := simplex.Solve(c, A, b)
// res.Status == simplex.Optimal
// res.Objective == 17/4, res.X == (5/4, 7/4), res.Dual == (1, 1, 0)
```

## 运行样例

```
$ go run ./cmd/demo
max  2*x1 + x2
s.t. x1 + x2 <= 3
     x1 <= 5/4
     x2 <= 2,  x1,x2 >= 0

basis exchanges (exact rational pivots):
  iter 1 (phase 2): x0 enters, x3 leaves, pivot = 1
  iter 2 (phase 2): x1 enters, x2 leaves, pivot = 1

status: Optimal (iterations: 2)
objective: 17/4
x = (5/4, 7/4)
dual y = (1, 1, 0)
optimality proof (strong duality):
  primal  c^T x = 17/4
  dual    b^T y = 17/4
  equal and exact => x* is globally optimal, max = 17/4
```

对偶价格证明：`y = (1, 1, 0)` 满足 `A^T y = (2, 1) = c`（对偶可行且紧），
`b^T y = 3 + 5/4 = 17/4 = c^T x`（强对偶），故 `x*` 为全局精确最优。

## 测试

```
$ go test ./... -v
=== RUN   TestOptimalExactRational          --- PASS   (验收：3 约束最大化，精确最优 17/4 + 对偶价格证明)
=== RUN   TestStrongDualityFractionalData   --- PASS   (分数数据强对偶)
=== RUN   TestUnbounded                     --- PASS   (无界射线证书)
=== RUN   TestInfeasible                    --- PASS   (Farkas 不可行证书)
=== RUN   TestPhase1Feasible                --- PASS   (负 RHS 触发两阶段)
=== RUN   TestCycling                       --- PASS   (Beale 循环用例，Bland 规则终止，最优 1/20)
=== RUN   TestDegenerateVertex              --- PASS   (退化顶点)
PASS
ok      simplex
```

每个最优性测试均验证：原始可行性、对偶可行性、互补松弛性与强对偶等式。

## 文件结构

- `simplex.go` — 求解器核心（两阶段单纯形、Bland 规则、对偶/证书提取）
- `simplex_test.go` — `go test` 测试套件
- `cmd/demo/main.go` — 运行样例（17/4 验收问题）
