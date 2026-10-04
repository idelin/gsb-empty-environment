# ratbezier — 有理贝塞尔曲线极值定位器

一维带权有理贝塞尔曲线的**精确**极值求解器（Go，仅标准库 `math/big`）。

## 算法

曲线 `y(t) = N(t)/D(t)`，其中 `N`、`D` 为 Bernstein 基下的多项式。

1. **导出整系数多项式**：`y'(t) = P(t)/D(t)^2`，其中
   `P(t) = N'(t)D(t) - N(t)D'(t)`。全部使用精确有理数运算构造，
   再通分为本原整系数多项式（`Poly.ToIntPoly`）。
2. **隔离 [0,1] 内所有变号根**（`IsolateRoots`）：
   - 区间 `(lo,hi)` 经 Möbius 变换 `t = lo+(hi-lo)/(1+s)` 后用
     Descartes 符号变化法则给出根数上界；为 0 剪枝，为 1 则精确二分
     收紧到宽 `2^-40` 的有理区间，否则对半递归；
   - 二分点 / 端点处 `P=0` 的根直接得到精确有理数；
   - 其余区间用**有理根定理**比对，命中即升级为精确有理根。
3. **极值分析**（`Curve.Analyze`）：临界点 = 端点 `t=0,1` + 全部内部根；
   相邻临界点之间用 `P` 在中点的符号判定上升/下降区间；
   在所有临界点处精确求值 `y(t)`（`big.Rat`），比较得到全局最大/最小。

正权重时 `D` 在 `[0,1]` 无零点，`sign(y') = sign(P)`，故极值点恰为
`P` 的根与端点，边界端点完整覆盖。

## 运行样例

```console
$ go run ./cmd/demo -points 0,1,0 -weights 1,2,1
control points y = [0, 1, 0]
weights        w = [1, 2, 1]
y(t) = N(t)/D(t) with
  N(t) = 4*t - 4*t^2
  D(t) = 1 + 2*t - 2*t^2
extremum equation P(t) = N'(t)D(t) - N(t)D'(t) = 0
  P(t) = 1 - 2*t   (integer coefficients)
sign-changing roots of P in [0,1]:
  t = 1/2 (exact rational)
monotonic segments:
  [0, 1/2]  increasing
  [1/2, 1]  decreasing
critical points (endpoints included):
  t = 0 (exact)     y = 0    [endpoint, global min]
  t = 1/2 (exact)   y = 2/3  [local max, global max]
  t = 1 (exact)     y = 0    [endpoint, global min]
GLOBAL MAX: y = 2/3 at t = 1/2
GLOBAL MIN: y = 0 at t = 0 and t = 1
```

无理极值点示例（`go run ./cmd/demo -points 0,2,1 -weights 1,2,1`）：
导出 `P(t) = 4 - 7*t + t^2`，根 `t = (7-√33)/2 ≈ 0.6277186767`
被隔离在宽度 `2^-40` 的精确有理区间内。

## 测试

```console
$ go test ./... -v
```

11 个测试覆盖：导数多项式精确构造、有理根精确隔离、无理根区间隔离、
有理根定理、精确全局极值（`2/3 @ t=1/2`）、单调曲线（端点即极值）、
端点处导数为零、无理极值数值对照、稠密采样交叉验证、常值曲线、
输入校验。

## 文件

- `poly.go` — 精确有理系数多项式：求值/求导/复合/Descartes 变换/整系数化
- `bezier.go` — 曲线定义、`N/D/P` 多项式导出、精确求值
- `isolate.go` — 变号根隔离（Descartes + 二分 + 有理根定理）
- `extrema.go` — 单调区间划分与全局极值分析
- `cmd/demo/main.go` — 命令行演示
