# proofcheck — 小型可验证证明步骤检查器

Python 3.11+，仅标准库（`fractions.Fraction` 精确有理数），不调用符号代数库。
对象为单变量有理系数多项式 Q[x]，内部表示为 `{指数: 系数}` 字典。

## 证明格式

```python
{
  "variable": "x",
  "steps": [
    {"rule": "init",       "expr": <ast>, "domain": "Q[x]"},
    {"rule": "expand",     "expr": <ast>, "domain": "Q[x]"},
    {"rule": "combine",    "expr": <ast>, "domain": "Q[x]"},
    {"rule": "diff",       "expr": <ast>, "domain": "Q[x]（多项式，非负整数指数，逐项幂法则）"},
    {"rule": "substitute", "expr": <ast>, "value": "1/2", "domain": "x ∈ Q（有理点代入）"},
  ],
}
```

表达式 AST：`{"num": "3/4"}`、`{"var": "x"}`、`{"add": [...]}`、
`{"mul": [...]}`、`{"neg": a}`、`{"pow": [base, n]}`（n 为非负整数）。

## 验证原则

- **不信任标签**：每步由验证器用规则独立重算，与声称结果逐项比对；
  标签正确但结论错、或结论对但规则错，都拒绝。
- **适用域**：每步必须带 `domain` 字段且与规则要求完全一致；
  验证器另独立检查前提（如 expand 需存在可展开结构、combine 结果须为
  规范形、substitute 的代入值须为有理数、pow 指数须非负整数）。
- **未知规则**：直接拒绝并报告步骤序号。
- **篡改定位**：任一系数被改，`ProofError.step_index` 即首个失败步骤。
- **最终结果**：`verify_proof` 返回最后一个通过验证的表达式的多项式。

## 运行样例

```
$ python3 proofcheck.py
合法证明验证通过，最终结果 = {0: Fraction(4, 1)}
篡改检测：首个失败步骤 = step 2 -> step 2 (combine): 合并结果与上一步表达式不相等
```

样例证明链：`(x+1)(x+2)` —expand→ `x²+2x+x+2` —combine→ `x²+3x+2`
—diff→ `2x+3` —substitute x=1/2→ `4`。

## 测试结果

```
$ python3 -m unittest -v
...
Ran 16 tests in 0.004s
OK
```

覆盖：合法证明通过、最终结果等于最后验证表达式、有理数精确计算
（(x+1/2)² 在 x=1/3 处 = 25/36）、逐步篡改定位、标签不可信（两个方向）、
未知规则/缺域/错域拒绝、combine 规范形检查、expand 适用性检查、
非有理代入值与负指数拒绝。
