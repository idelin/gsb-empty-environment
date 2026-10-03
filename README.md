# ratbezier — 有理贝塞尔曲线极值定位器

纯 Go 标准库实现（`math/big` 精确有理数/整数运算，无浮点误差累积）。
输入带权重的一维有理贝塞尔曲线（3 个或更多控制点），在参数区间
`[0, 1]` 上严格求出所有上升/下降区间与全局最大/最小值。

## 算法

曲线写成 `B(t) = P(t) / W(t)`，其中 `P`、`W` 是 Bernstein 基下的多项式：

1. **导出多项式**：导数分子 `N(t) = P'(t)W(t) - P(t)W'(t)`。
   权重为正时分母 `W(t)^2 > 0`，故 `sign(B') = sign(N)`。
   通分（系数分母 LCM）并除以 content，得到**整系数本原多项式**（保号）。
2. **有理根提取**：用有理根定理（`p/q` 根的分子整除常数项、分母整除首项）
   反复提取全部有理根并做精确综合除法，得到**精确的有理数临界点**。
3. **无理根隔离**：对剩余无有理根的余因子，用 **Sturm 序列**计数
   `(a, b]` 内不同实根数，配合二分把每个根隔离进宽度 `< eps`
   （默认 `1e-30`）的有理区间。
4. **单调性与极值**：在相邻临界点之间取有理采样点判定 `N` 的符号，
   得到严格上升/下降区间；端点 `t=0`、`t=1` 始终纳入候选，
   比较所有候选处的精确函数值得到全局极值。

所有计算均为精确有理运算；无理临界点以「隔离区间 + 任意精度有理近似」
输出，有理临界点以精确分数输出。

## 用法

```go
c, _ := ratbezier.NewCurve([]float64{0, 2, 1}, []float64{1, 2, 1})
res := c.Analyze(nil) // nil = 默认精度 1e-30
fmt.Println(res.Derivative)  // t^2 - 7t + 4
fmt.Println(res.Max.Value)   // 全局最大值（精确有理数）
```

也支持精确输入 `NewCurveRat(points, weights []*big.Rat)`。

运行样例与测试：

```sh
go run ./cmd/demo
go test ./...
```

## 运行样例（`go run ./cmd/demo` 摘要）

```
=== Example 1: exact rational critical point ===
control points: [0 1 0], weights: [1 2 1]
derivative numerator polynomial N(t) = -2t + 1
critical points in (0, 1):
  t = 1/2 (approx 0.5)
monotonicity on [0, 1]:
  [0, 1/2]: increasing
  [1/2, 1]: decreasing
GLOBAL MAX: value 2/3 (approx 0.666666666666667) at t = 1/2
GLOBAL MIN: value 0 at t = 0

=== Example 2: irrational critical point ===
control points: [0 2 1], weights: [1 2 1]
derivative numerator polynomial N(t) = t^2 - 7t + 4
critical points in (0, 1):
  t = irrational in [0.627718676731, 0.627718676731], approx 0.627718676730986
      （即 (7 - sqrt(33)) / 2，隔离区间宽度 < 1e-30）
monotonicity on [0, 1]:
  [0, 0.6277...]: increasing
  [0.6277..., 1]: decreasing
GLOBAL MAX: value 1.54257289224366 (exact rational) at t = 0.627718676730986
GLOBAL MIN: value 0 at t = 0

=== Example 3: monotone curve ===
control points: [0 1 2], weights: [1 1 1]
derivative numerator polynomial N(t) = 1
critical points in (0, 1): (none)
monotonicity on [0, 1]: [0, 1]: increasing
GLOBAL MAX: value 2 at t = 1
GLOBAL MIN: value 0 at t = 0
```

## 测试结果（`go test -v ./...`）

20 个测试全部通过，覆盖：多项式运算/本原化、有理根提取（不同根、
重根、无根）、Sturm 计数、根隔离（√2、多根分离）、精确/无理临界点
分析、单调/递减/常数曲线、高次曲线、输入校验、端点覆盖。

```
--- PASS: TestDerivativePolynomial
--- PASS: TestEvalExact
--- PASS: TestAnalyzeExactRationalCriticalPoint
--- PASS: TestAnalyzeIrrationalCriticalPoint
--- PASS: TestAnalyzeMonotone
--- PASS: TestAnalyzeDecreasing
--- PASS: TestAnalyzeConstant
--- PASS: TestAnalyzeHigherDegree
--- PASS: TestNewCurveValidation
--- PASS: TestPolyArithmetic
--- PASS: TestPolyDeriv
--- PASS: TestPolyEvalRat
--- PASS: TestPolyPrimitive
--- PASS: TestPolyString
--- PASS: TestExtractRationalRootsDistinct
--- PASS: TestExtractRationalRootsRepeated
--- PASS: TestExtractRationalRootsNone
--- PASS: TestSturmCountRoots
--- PASS: TestIsolateRootsSqrt2
--- PASS: TestIsolateRootsMultiple
PASS
ok  	ratbezier
```

## 代码结构

- `poly.go` — 整系数多项式（运算、求导、content/本原化、有理点求值）
- `ratpoly.go` — 有理系数多项式（除法余式、通分转整系数）
- `sturm.go` — Sturm 序列与区间实根计数
- `roots.go` — 有理根定理提取 + Sturm/二分根隔离
- `curve.go` — 曲线定义、导数分子导出、`Analyze` 极值/单调性分析
- `cmd/demo/main.go` — 三个演示样例
