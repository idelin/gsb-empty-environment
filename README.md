# ratsimplex — 纯有理数单纯形法 LP 求解器

用 Go 标准库（仅 `math/big`）实现的精确单纯形法线性规划求解器。
所有迭代（基变量置换、既约费用、比值检验）均在 `*big.Rat` 上进行，
**全程无有理数精度损失**，不使用任何浮点运算。

## 问题形式

```
maximize   C^T x
subject to A x <= B
           x >= 0
```

## 算法

- **两阶段单纯形法**：第一阶段引入人工变量处理负右端项（`b_i < 0`），
  求初始可行基；第二阶段优化真实目标。
- **Bland 规则**选择进基/出基变量，保证退化情形下有限步终止
  （测试中含 Beale 经典循环算例）。
- **输出**：
  - `Optimal`：精确最优值、原始解向量 `x`、对偶变量（影子价格）`y`，
    满足强对偶 `C^T x = B^T y` 与互补松弛；
  - `Unbounded`：可行点 + 射线 `d`（`d >= 0`、`A d <= 0`、`C^T d > 0`）；
  - `Infeasible`：Farkas 证书 `y`（`y >= 0`、`y^T A >= 0`、`y^T B < 0`）。

## 运行测试

```sh
go test -v ./...
```

测试结果（全部通过）：

```
=== RUN   TestMaximizeThreeConstraints   # 3 约束最大化，精确最优值 17/4
--- PASS: TestMaximizeThreeConstraints
=== RUN   TestExactThirds                # 1/3 类分数，二进制浮点无法精确表示
--- PASS: TestExactThirds
=== RUN   TestUnbounded                # 无界证书：可行点 + 射线
--- PASS: TestUnbounded
=== RUN   TestInfeasible               # Farkas 不可行证书
--- PASS: TestInfeasible
=== RUN   TestPhaseOneFeasible         # 负右端项触发第一阶段
--- PASS: TestPhaseOneFeasible
=== RUN   TestBealeCycling             # Beale 循环算例，Bland 规则终止于 1/20
--- PASS: TestBealeCycling
=== RUN   TestDegenerateRedundant      # 冗余约束行的处理
--- PASS: TestDegenerateRedundant
PASS
ok      ratsimplex
```

## 运行样例

```sh
go run ./cmd/lpdemo
```

输出：

```
=== maximize 2x + 3y subject to 3 inequality constraints ===
status: Optimal
objective: 17/4
primal x:  (1/4, 5/4)
dual y:    (1, 1, 0)
strong duality: C^T x = 17/4 = B^T y = 17/4

=== unbounded example: max x + y, x - y <= 1 ===
status: Unbounded
feasible point: (1, 0)
ray:            (1, 1)
(objective grows without bound along point + t*ray, t >= 0)

=== infeasible example: x + y <= 1, x + y >= 2 ===
status: Infeasible
Farkas certificate y: (1, 1)
(y >= 0, y^T A >= 0, y^T B < 0 proves infeasibility)
```

## 验收算例（最大值为 17/4）

```
max 2x + 3y
s.t. x +  y <= 3/2
     x + 2y <= 11/4
     3x +  y <= 3
     x, y >= 0
```

- 精确最优解 `x* = (1/4, 5/4)`，最优值 `2·(1/4) + 3·(5/4) = 17/4`；
- 对偶价格 `y* = (1, 1, 0)`：对偶可行（`A^T y >= C`、`y >= 0`）且
  `B^T y* = 3/2 + 11/4 = 17/4 = C^T x*`，由强对偶定理即证 `17/4` 为全局最优；
- 测试 `TestMaximizeThreeConstraints` 以 `big.Rat` 精确断言上述全部结论，
  并验证互补松弛性 `y_i·(A_i x − b_i) = 0`。

## 文件结构

- `simplex.go` — 求解器（包 `simplex`）：两阶段单纯形、Bland 规则、证书提取
- `simplex_test.go` — `go test` 验收与边界测试
- `cmd/lpdemo/main.go` — 运行样例
