# symrewrite：基于模式规则的符号表达式重写系统

纯 Python 3.11 标准库实现（仅 `unittest` 做测试），不依赖任何符号代数库。
有理数用 `fractions.Fraction` 精确计算，全程不经过浮点。

## 功能

- **模式替换**：规则 = 模式（含 `PVar` 模式变量）+ 替换模板/函数，结构化确定性匹配。
- **规则优先级**：`priority` 数值大者优先；同优先级按名称排序，保证确定性。
- **条件与适用域**：`condition` 不满足时规则不可用（如除零、0^0）；每条规则带
  `domain` 文字说明，记录进变换链的每一步。
- **循环检测**：每步后对表达式取规范形（拍平并排序可交换的 Add/Mul），
  规范状态重复即报告 `cycle` 及循环区间（如 a→b、b→a）。
- **步数耗尽**：超过 `max_steps` 报告 `step_limit`。
- **完整变换链**：每步记录规则名、局部位置（子节点下标路径）、绑定、
  前后表达式、适用域、被条件拦下的规则。
- **可回放**：`replay(initial, steps, rules)` 按记录的位置与规则逐步重放并校验。

## 结构

- `symrewrite/expr.py` — 不可变 AST（Num/Sym/Add/Mul/Div/Pow）、规范形、定位与局部替换
- `symrewrite/pattern.py` — `PVar` 模式匹配与模板替换
- `symrewrite/rules.py` — `Rule` 与内置规则集（含 a→b、b→a 演示规则）
- `symrewrite/engine.py` — 重写引擎、循环/步数检测、回放
- `examples/demo.py` — 运行样例
- `tests/test_rewrite.py` — 15 个 unittest 用例

## 运行

```bash
python3 examples/demo.py                 # 运行样例
python3 -m unittest discover -s tests -v # 运行测试
```

## 样例输出（节选）

```
【例1 精确有理数 + 完整变换链】
  初始: ((1/2 + 1/3) * (y / y))
  步骤 1 | 规则 fold-add | 位置 (0,) | 绑定 a=1/2, b=1/3
         适用域: a、b 为有理数；Fraction 精确加法
         ((1/2 + 1/3) * (y / y))  ⇒  (5/6 * (y / y))
  步骤 2 | 规则 div-same | 位置 (1,) | 绑定 x=y
         适用域: x ≠ 0；相同非零表达式相除为 1
         (5/6 * (y / y))  ⇒  (1 * 5/6)
  步骤 3 | 规则 fold-mul | 位置 () | 绑定 a=1, b=5/6
         (1 * 5/6)  ⇒  5/6
  终止: normal_form（无可用规则，已达范式）
  结果: 5/6
  回放校验: 通过

【例6 循环检测：a→b、b→a】
  初始: a
  步骤 1 | 规则 swap-ab | a  ⇒  b
  步骤 2 | 规则 swap-ba | b  ⇒  a
  终止: cycle（检测到重复规范状态，存在循环）
  循环: 状态首次出现于第 0 步，第 2 步重复
```

## 测试结果

```
Ran 15 tests in 0.003s
OK
```
