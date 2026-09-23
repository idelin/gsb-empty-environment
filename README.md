# proofcheck：小型证明步骤格式与独立验证器

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数运算），
不调用任何符号代数库。测试使用 `unittest`。

## 表达式格式

表达式为纯 dict（可 JSON 序列化）：

```json
{"const": "3/4"}        // 有理数常量（字符串或整数，禁止浮点）
{"var": "x"}            // 变量
{"add": [e1, e2, ...]}  // 求和
{"mul": [e1, e2, ...]}  // 求积
{"pow": [e, n]}         // 非负整数次幂
```

## 证明格式

```json
{
  "variable": "x",
  "steps": [
    {"rule": "expand",        "domain": "...", "input": E, "output": E},
    {"rule": "combine",       "domain": "...", "input": E, "output": E},
    {"rule": "differentiate", "domain": "...", "var": "x", "input": E, "output": E},
    {"rule": "substitute",    "domain": "...", "var": "x", "value": "3/2",
     "input": E, "output": E}
  ],
  "result": E
}
```

## 验证规则

1. **独立验证**：每步由检查器根据 `input` 独立重算变换结果，再与声称的
   `output` 比较；绝不凭 `rule` 标签直接信任结论。
   - `expand`：值不变，且输出为展开形（单项式之和）；
   - `combine`：值不变，输出为展开形且每个次数至多出现一次；
   - `differentiate`：按幂法则独立求导后比较；
   - `substitute`：代入有理数值独立求值后比较。
2. **适用域**：每步必须携带非空 `domain` 说明；验证报告同时给出该规则的
   规范适用域。
3. **证明链**：每步 `input` 必须等于上一步已验证的 `output`。
4. **最终结果**：`result` 必须等于最后验证表达式。
5. **未知规则**：直接拒绝，并报告首个失败步骤编号。

## 运行

```sh
python3 demo.py                  # 运行样例（含篡改定位与未知规则演示）
python3 -m unittest discover -v  # 运行测试
```
