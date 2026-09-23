# simsub — 多变量多项式的同时代换

纯 Python 3.11 标准库实现（`fractions` / `ast` / `math` / `dataclasses`），
不调用任何符号代数库。系数全程为 `Fraction` 精确有理数。

## 功能与保证

- **同时代换**：一次性遍历表达式 AST，叶子变量各查一次映射表，右值整体植入、
  互不递归。因此 `x→y、y→x` 是交换（`x + 2y → 2x + y`），不会像顺序代换那样
  塌缩成 `3x`。
- **代换零消项**：映射使因子为 0 时按零吸收律消去整项（`(x-y)*z` 在 `x→y` 下
  直接消失），同类项相消在构造时即时完成。
- **膨胀估计 + 项数预算**：展开前用纯结构性公式给出项数的保守上界
  （加法取和、乘法取积、`Pow(b,n)` 取 `C(deg(b)·n + v, v)`，v 为变量数），
  上界超预算即停止；展开过程中另有硬上限双保险。
- **预算中断不返回截断假等式**：中断时返回**未展开的精确 AST**
  （`expanded=False, poly=None`），它与原多项式严格相等，只是保持未展开形态；
  绝不返回“部分展开结果”。
- **每步变换说明适用域**：结果对象的 `steps` 记录每步的说明与适用域。

## 适用域

| 步骤 | 适用域 |
| --- | --- |
| 同时代换 | Q[x1,...,xn] 上的环同态；映射值可为任意多项式（含变量） |
| 规范化/零消项 | Q[x1,...,xn]；Q 特征为 0，系数相等/相消可精确判定 |
| 膨胀估计 | 组合上界，恒 ≥ 真实项数（保守，宁停勿错） |
| 展开 | Q[x1,...,xn]，Fraction 精确运算；幂指数为非负整数 |
| 解析 | `+ - * / **` 与括号；分母须为非零整数常数；禁浮点、禁负指数 |

## 用法

```python
from simsub import substitute_string, format_poly, format_expr

r = substitute_string("x + 2*y", {"x": "y", "y": "x"})
assert format_poly(r.poly) == "2*x + y"          # 交换而非塌缩

r = substitute_string("(x + y + z)**10", {"x": "a + b"}, budget=100)
assert not r.expanded                             # 预算中断
print(format_expr(r.result))                      # (a + b + y + z)**10 —— 精确未展开形式
for s in r.steps:
    print(s.note, "|", s.domain)                  # 每步说明 + 适用域
```

## 运行

```bash
python3.11 demo.py                        # 运行样例
python3.11 -m unittest discover -s tests  # 运行测试（21 个）
```

运行样例输出与测试结果见 `SAMPLES.md`。

## 文件

- `simsub/core.py` — 全部实现（AST、解析、同时代换、项数上界、精确展开、格式化）
- `demo.py` — 四个运行样例（交换 vs 顺序污染、零消项、有理精确、预算中断）
- `tests/test_simsub.py` — 21 个 unittest 用例（含随机化可靠性测试）
