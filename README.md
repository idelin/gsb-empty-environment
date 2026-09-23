# 截断有理幂级数（TPS）

Python 3.11+，仅用标准库（`fractions.Fraction` 精确有理数），不依赖符号代数库。
`TPS(coeffs, order)` 表示模 `x^order` 意义下的截断幂级数。

## 运算与适用域

| 运算 | 适用域 | 截断阶数传播 |
|---|---|---|
| `add` / `sub` | 两级数阶数必须相同，否则 `ValueError` | 结果阶数 = 共同阶数 |
| `mul(other, order=None)` | 任意 | 结果 = 指定 `order`，缺省 `min(两阶数)`；只保留指定阶 |
| `inverse(order=None)` | 常数项 `a_0 != 0`，否则 `ValueError`（绝不返回零系数蒙混） | 缺省 = 自身阶数 |
| `compose(inner, order=None)` | 内层常数项必须为 0，否则 `ValueError` | 缺省 = `min(两阶数)`，Horner 法逐步截断 |

保证：`f.mul(f.inverse(), order=N) == TPS.one(N)`（截断范围内乘积为 1）。

## 运行

```sh
python3 demo.py            # 运行样例
python3 -m unittest test_tps -v   # 22 个测试全部通过
```
