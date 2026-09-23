# 截断有理幂级数

本项目使用 Python 3.11 标准库实现模 \(x^N\) 的有理系数形式幂级数：

\[
f(x)=a_0+a_1x+\cdots+a_{N-1}x^{N-1}\pmod{x^N}.
\]

所有系数都使用 `fractions.Fraction` 精确表示，不使用任何符号代数库。

## 文件

- `rational_power_series.py`：核心类型与加法、乘法、乘法逆、复合。
- `test_rational_power_series.py`：`unittest` 测试。
- `example.py`：可运行样例。

## 阶数传播与适用域

设 `f.order = A`，`g.order = B`，表示只知道二者模 \(x^A\)、\(x^B\) 的系数。

| 变换 | 调用 | 输出阶数 | 适用域 |
| --- | --- | --- | --- |
| 表示 | `TruncatedPowerSeries(coeffs, order=N)` | `N` | `N >= 0`；系数必须是精确有理数，如 `int` 或 `Fraction` |
| 加法 | `add(f, g)` | `min(A, B)` | 只保留双方都确定的低次项 |
| 乘法 | `multiply(f, g, order=N)` | `N` | `0 <= N <= min(A, B)`；卷积中次数 `>= N` 的项直接丢弃 |
| 乘法逆 | `inverse(f, order=N)` | `N` | `1 <= N <= A` 且 `f[0] != 0`；`f[0] == 0` 时抛出 `ZeroConstantError` |
| 复合 | `compose(f, g, order=N)` | `N` | `0 <= N <= min(A, B)`；当 `N > 0` 时必须有 `g[0] == 0`，否则抛出 `NonZeroConstantError` |

`inverse` 和 `compose` 不显式传 `order` 时，分别使用输入阶数或两个输入阶数的最小值。

### 逆元恒等

当 `f[0] != 0` 时，递归计算逆元：

\[
b_0=a_0^{-1},\qquad
b_n=-a_0^{-1}\sum_{k=1}^{n}a_kb_{n-k}.
\]

因此对每个合法的 `1 <= N <= f.order`：

\[
f(x)f^{-1}(x)\equiv 1\pmod{x^N}.
\]

### 复合的常数条件

一般截断幂级数复合要求内层序列 `g[0] == 0`。此时输出 \(n\) 次系数只依赖外层 \(0\) 到 \(n\) 次系数。若 `g[0] != 0`，外层未知的高次项也可能影响低次结果，因此输入信息不足，代码抛出异常而不是猜测。

## 运行

```bash
python3.11 example.py
python3.11 -m unittest -v
```

## 样例输出

```text
f: [1, -1/2, 2, -3/4, 0] mod x^5
h: [2, 1, 0, 0] mod x^4
g: [0, 1, 1/2, -1, 0] mod x^5
f + h (order min(5, 4)=4): [3, 1/2, 2, -3/4] mod x^4
f * h retained only through order 3: [2, 0, 7/2] mod x^3
f^-1: [1, 1/2, -7/4, -9/8, 53/16] mod x^5
f * f^-1 modulo x^5: [1, 0, 0, 0, 0] mod x^5
f(g(x)) modulo x^5: [1, -1/2, 7/4, 7/4, -37/8] mod x^5
illegal inverse: a series with zero constant term has no multiplicative inverse
illegal composition: composition of truncated series requires inner constant term to be zero
```

## 测试结果

```text
Ran 16 tests in 0.001s

OK
```
