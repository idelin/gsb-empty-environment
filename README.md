# sturmiso — 有理系数单变量多项式的 Sturm 实根隔离（精确、可验证）

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理运算），**不调用任何
符号代数库**。对任意 `f ∈ Q[x]` 隔离全部**不同实根**，重根只隔离一次、重数
单独列出；每个区间附带可独立重验的根数证书。

## 流水线与适用域

每一步变换的适用域都标注在对应模块 docstring 中：

1. **平方自由分解**（`squarefree.py`，Yun 算法）：适用任意非常数 `f ∈ Q[x]`
   （特征 0）。得 `f = c·∏ f_k^k`，`f_k` 两两互素、平方自由；`f_k` 的根即 `f`
   中重数恰为 `k` 的根。例：`x²` 只有不同根 `0`，重数 2 另列于 `f_2 = x`。
2. **根式 `R = ∏ f_k`**（首一、平方自由）：其实根集 = `f` 的不同实根集。
3. **Sturm 序列**（`sturm.py`）：`p₀=R, p₁=R', p_{i+1} = -rem(p_{i-1}, p_i)`，
   适用域为 `R` 平方自由（非平方自由输入会显式抛错）。
4. **柯西界播种**（`isolate.py`）：`B = 1 + max|a_i|/|a_n|`，全部实根严格落在
   开区间 `(-B, B)` 内，故默认种子端点永不为根。
5. **预算二分隔离**：`budget` 为最大二分次数；耗尽时返回已隔离根与
   `pending` 未决区间（各附根数证书），可用 `resume_isolation` 续算。

## 端点恰为根的精确计数规则

对平方自由多项式，`V(x)` 为 Sturm 序列在 `x` 处的变号数（零项跳过）。
跨越 `p` 的根时 `V` 恰好减 1，因此在根 `x₀` 处删去零项得到的是**右侧**
变号数 `V(x₀⁺)`，而 `V(x₀⁻) = V(x₀⁺) + 1`。于是（不猜任何 ε）：

| 区间 | 根数 |
|---|---|
| `[a, b]` | `V(a⁻) − V(b⁺)` |
| `(a, b]` | `V(a⁺) − V(b⁺)` |
| `[a, b)` | `V(a⁻) − V(b⁻)` |
| `(a, b)` | `V(a⁺) − V(b⁻)` |

端点根在闭端点被计数、在开端点不被计数，全库一致。隔离时若种子闭端点
恰为根，则先剥离为点根 `{r}` 并把端点打开，绝不重复计数；二分中点恰为
根时同样处理为点根 `{mid}`。

## 每区间证书

每个隔离区间 / 未决区间携带 `RootCountCertificate`：

- 区间（有理端点、开闭标志）；
- Sturm 链次数、两个端点的符号向量（`EndpointSign`，根端点记录被丢弃的
  零项下标与单侧方向）；
- `count = V(下端) − V(上端)` 的算术规则文本；
- 隔离根另附归属的平方自由因子（含重数）及每个因子的计数证书。

`verify.py` 是**独立验证器**：只从多项式与证书出发，重算平方自由性、精确
重数、互素性、乘积复原、Sturm 链、端点符号、区间计数、因子归属、区间
两两不相交与（种子全覆盖时的）全根总数，以及修订报告中的每条再利用
证书与匹配边。

## 参数修订：区间再利用与根身份匹配

`revise_polynomial(old_result, new_f, budget=)`：

1. 对新多项式做完整（预算受限、可续算）隔离；
2. **再利用**每个旧隔离区间作为新根式的查询区域，给出新的认证根数
   （`ReusedInterval`），无需在旧区间重新二分；
3. 根身份**只**由认证包含关系连接：两区间交集内新旧根数均 ≥1（各自
   认证），或一区间的证书嵌套于另一区间。**绝不**用区间中心距离或参数
   步长猜唯一身份。

匹配边构成二部图，连通分量分类为 `one_to_one` / `split`（1 旧对 k 新）/
`merge`（k 旧对 1 新）/ `many_to_many` / `old_only` / `new_only`。
重根分裂与根合并都以**组**的形式报告，绝不虚构一一身份。

## 运行

```bash
python3.11 demo.py      # 演示：隔离/重根/空根/端点根/预算中断续算/修订匹配
python3.11 -m unittest  # 30 个测试
```

## 运行样例（节选自 `python3.11 demo.py`）

```
--- 2. repeated roots: x^2 * (x-1)^3 ---
   square-free factors: f_2(x) = x (multiplicity 2), f_3(x) = x - 1 (multiplicity 3)
   radical R(x) = x^2 - x
   root #1: interval {0}, multiplicity 2, factors [2]
      cert: V(lower:left)=2 signs=(-1, 1) | V(upper:right)=1 signs=(-1, 1) -> count=1
   root #2: interval (0, 2), multiplicity 3, factors [3]
   independent verifier: True

--- 4. exact endpoint-root counting for x*(x-1) ---
   roots of x(x-1) in [0, 1] = 2
   roots of x(x-1) in (0, 1) = 0
   roots of x(x-1) in [0, 1) = 1
   roots of x(x-1) in (0, 1] = 1

--- 5. budget interruption and resume: (x-1)(x-2)(x-3)(x-4) ---
   PENDING [0, 51): certified count = 4
   ...resume...
   root #1: interval [0, 51/32) ... root #4: interval [51/16, 51/8)
   independent verifier: True

--- 7. revision: irrational root splits ---
    x^2 - 2  ->  x^2 - 2x + 9999/10000 (roots 1 +/- 1/100)
   reused old interval [0, 3): certified new count = 2
   group split: old(2,) new(1, 2)
   group old_only: old(1,) new()

--- 8. revision: two roots merge (reverse direction) ---
   group merge: old(1, 2) new(2,)
   group new_only: old() new(1,)
```

## 测试结果

```
$ python3.11 -m unittest
Ran 30 tests in 0.026s
OK
```

覆盖：多项式运算/gcd/本原化、平方自由分解（含 `x²`、常数、零多项式）、
端点根四种开闭组合、点区间、无界区间、非平方自由拒绝、隔离（无理根、
重根重数另列、无实根返回空、端点根种子、区间两两不相交、预算 0/1 中断
与续算、证书篡改检测）、修订（精确有理匹配、分裂成组、合并成组、恒等
修订、根出现/消失、修订中预算中断）。

## 模块

- `sturmiso/poly.py` — Q[x] 精确运算（加减乘除、导数、gcd、本原整数化）
- `sturmiso/squarefree.py` — Yun 平方自由分解与根式
- `sturmiso/sturm.py` — Sturm 链、端点符号、精确端点根计数
- `sturmiso/intervals.py` — 有理区间（开闭/无界/点）与精确交、包含
- `sturmiso/certificates.py` — 根数证书数据结构
- `sturmiso/isolate.py` — 预算隔离引擎、柯西界、续算
- `sturmiso/revision.py` — 区间再利用与保守根身份匹配
- `sturmiso/verify.py` — 独立验证器
- `demo.py` / `test_sturmiso.py` — 样例与测试
