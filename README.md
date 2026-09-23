# 有理函数 GCD 约分与定义域管理

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理运算），
不调用任何符号代数库。

## 功能

- 单变量多项式欧几里得 GCD，分子分母约分；
- 定义域保存为**原分母**实根排除集合，约分不丢失可去奇点
  （`x/x` 化简为 `1` 但仍排除 `x=0`）；
- 分母首项系数规范为首一（隐含首项符号规范）；
- 平方自由分解（Yun 算法，特征 0）；
- 无理排除点用**有理隔离区间**描述（Sturm 定理 + 二分），
  有理根由有理根定理精确提取，不要求闭式根表达；
- 加/减/乘/除组合运算后排除集合取并集并重新约分；
- 独立验证：代数恒等式（交叉相乘）+ 排除集合合法性 + 有理采样对照；
- 每步变换在 `history` 中记录说明及适用域；
- 零分母拒绝构造；零函数化简为 `0/1` 但保留排除集合。

## 文件

- `polyutil.py` — 多项式精确运算、GCD、平方自由分解、Sturm 实根隔离
- `rational.py` — `RationalFunction`：约分、排除集合、组合运算、变换历史
- `verify.py` — 独立验证（恒等式 / 排除集合 / 有理采样）
- `demo.py` — 运行样例
- `test_ratsimp.py` — unittest 测试（重根、公共因子、零函数等）

## 运行

```sh
python3.11 demo.py              # 运行样例
python3.11 -m unittest -v       # 运行测试
```

## 用法示例

```python
from rational import RationalFunction

f = RationalFunction([0, 1], [0, 1])   # x / x
print(f.num, f.den)                    # [Fraction(1)] [Fraction(1)]
print(f.exclusions)                    # [Fraction(0)] —— x=0 仍被排除
f.evaluate(3)                          # Fraction(1)
f.evaluate(0)                          # 抛出 DomainError
```
