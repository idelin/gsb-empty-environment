# rational-monotone-cubic

保单调的**有理分段三次插值**（Delbourgo–Gregory 族），TypeScript 实现，零依赖。

- 输入节点单调（非减或非增）时，**每一段都解析地保持单调**——不靠采样点检查；
- 端点导数规则固定（Fritsch–Carlson 单侧三点式 + 限幅），内部节点用 Fritsch–Butland 加权调和平均；
- 重复横坐标：纵坐标相同则合并，**纵坐标不同则拒绝**（抛 `DuplicateAbscissaError`）；
- 每段输出完整系数与**导数非负的解析证据**（Bernstein 系数证书）；
- 附带**精确二次导数根分析**（BigInt 有理数 + Sturm 序列 + 判别式），用于核对平台、陡变、非均匀间距等情形；
- 非单调输入自动退化为"逐段单调、无过冲"插值（见下文）。

## 数学构造

段内局部参数 `θ = (x - x0)/h ∈ [0,1]`，`h = x1 - x0`，`Δ = (y1 - y0)/h`：

```
P(θ) = N(θ)/D(θ)
N(θ) = y1·θ³ + (r·y1 - h·d1)·θ²(1-θ) + (r·y0 + h·d0)·θ(1-θ)² + y0·(1-θ)³
D(θ) = 1 + (r - 3)·θ(1-θ)
```

`d0, d1` 为端点导数。记规范化斜率 `α = d0/Δ`、`β = d1/Δ`（斜率限幅规则保证 `α, β ≥ 0`），形状参数取

```
r = 1 + α + β
```

此时两个恒等式使单调性证明是初等的：

1. **N 的三次项恒为零**：`(r-1)(y0-y1) + h(d0+d1) = 0`，于是导数分子恰好是**二次**多项式

   ```
   dP/dx = Δ · M(θ) / D(θ)²,   M(θ) = α(1-θ)² + 2θ(1-θ) + βθ²
   ```

   其 Bernstein 系数为 `[α, 1, β]`，全部非负（且在 (0,1) 内部 `M > 0`）。
   精确根分析归结为二次判别式 `disc = 4(1 - αβ)`。

2. **分母正性**：`D(θ) = 1 + (α+β-2)θ(1-θ) ≥ min(1, (α+β+2)/4) ≥ 1/2 > 0`。

因此 `dP/dx` 在整个区间上与 `Δ` 同号：数据单调 ⇒ 每段单调。每段通过
`segment.evidence` 输出机器可核查的证据：

```ts
{
  kind: 'rational',
  sign: 1,                      // Δ 的符号
  bernstein: [alpha, 1, beta],  // M(θ) 的 Bernstein 系数，全 ≥ 0
  denominatorMin: 0.5..1,       // D(θ) 在 [0,1] 上的正下界
  ...
}
```

常数段（`Δ = 0`，此时斜率规则强制 `d0 = d1 = 0`）退化为常值，证据为 `kind: 'constant'`。

## 导数规则（固定）

- **内部节点**（Fritsch–Butland）：相邻割线斜率异号或其一为零 ⇒ `d_i = 0`；否则
  `d_i = (w1+w2) / (w1/Δ_{i-1} + w2/Δ_i)`，`w1 = 2h_i + h_{i-1}`，`w2 = h_i + 2h_{i-1}`。
- **端点**（Fritsch–Carlson 单侧三点式，固定不对外开放）：
  `d = ((2h0+h1)Δ0 - h0Δ1)/(h0+h1)`；若与 `Δ0` 异号则取 0；若 `Δ0, Δ1` 异号且
  `|d| > 3|Δ0|` 则限幅为 `3Δ0`。右端点对称。两个节点时 `d0 = d1 = Δ0`。

## 输入校验

- 横坐标必须非递减；**重复横坐标**：纵坐标相同 → 静默合并；纵坐标不同 → 抛
  `DuplicateAbscissaError`（拒绝，因为插值问题无解）。
- 非有限输入（`NaN`/`Infinity`）抛错；去重后不足两个节点抛错。
- `evaluate`/`derivative` 在插值范围之外抛 `RangeError`（有理形式外推可能碰到分母零点，故禁止）。

## 非单调输入的处理

默认（`requireMonotone: false`）：算法对任意数据合法。Fritsch–Butland 规则在
局部极值点强制 `d_i = 0`，且每个节点导数与相邻割线同号，因此**每一段都沿该段
割线方向单调**（升段递增、降段递减、平段常值），绝不产生过冲——这正是 PCHIP
族的标准行为。`spline.isNonDecreasing` / `spline.isNonIncreasing` 报告输入的单调性。

若传入 `requireMonotone: true`，非单调数据直接抛 `NonMonotoneDataError`。

## 精确根分析（非采样验证）

`src/exact.ts` 用 BigInt 精确有理数（每个 double 转为其精确二进制有理值）从
**证书值** `α, β, r, y0, y1` 重建导数分子
`M(θ) = (y1-y0)·[α(1-θ)² + 2θ(1-θ) + βθ²]`（恰为二次），并：

1. 转成 Bernstein 形式，系数全 ≥ 0 即证明 `M ≥ 0` 在整个区间成立；
2. 计算二次**判别式**（恒等于 `(y1-y0)²·4(1-αβ)`）；
3. 用 **Sturm 序列**精确计数 `M` 在开区间 (0,1) 内的不同实根——零个根独立确认无变号。

`spline.verifyMonotonicity()` 对全部段执行上述分析并返回报告；`certified === true`
表示三种证据互相印证。测试套件用它对**平台（连续平段）、陡变（相邻纵差 1e9 倍）、
非均匀间距（步长比 1:100）**等数据逐段核对。

证书刻画的是设计有理函数；浮点求值器与之仅差舍入误差（测试以紧相对容差核对
输出的幂基系数与证书函数一致）。

## 用法

```ts
import { createMonotoneRationalSpline } from './src/index.ts';

const spline = createMonotoneRationalSpline(
  [0, 0.1, 0.2, 0.9, 3.7, 10],
  [0, 3, 3.5, 4, 4.25, 5],
);

spline.evaluate(1.5);            // 插值
spline.derivative(1.5);          // 导数
spline.segments[2].coeffs;       // 第 2 段系数（分子/分母/α/β/r）
spline.segments[2].evidence;     // 导数非负证据（Bernstein 证书）
spline.verifyMonotonicity();     // 全段精确根分析报告
```

## 运行测试

需要 Node ≥ 22.6（`--experimental-strip-types`）或 ≥ 23.6（原生运行 TypeScript）：

```sh
node --test test/
```
