# set-solver

纯标准库（Python 3.11）的有限域集合变量约束求解器，不调用 SAT/SMT 库。

## 模型

每个集合变量维护两个界：

- **lower（下界）**：必含元素（required members）
- **upper（上界）**：可能元素（possible members）

一旦 `lower ⊄ upper`、`|lower| > max_card` 或 `|upper| < min_card` 即抛出
`Conflict`。可能成员永远不会被当作必含成员。

## 约束与传播

- `SubsetOf(a, b)`：`a.upper &= b.upper`，`b.lower |= a.lower`
- `UnionEq(a, b, c)`：`c = a ∪ b`；下界并、上界并双向收缩；`c` 中必含元素
  若只能来自一个操作数，则强制该操作数包含它
- `InterEq(a, b, c)`：`c = a ∩ b`；**下界 = lower(a) ∩ lower(b)，
  上界 = upper(a) ∩ upper(b)，分别计算**；`x ∈ lower(a)` 且 `x ∉ upper(c)`
  则 `x` 从 `upper(b)` 移除
- 基数约束：`min_card`/`max_card` 在 `SetVar.tighten()` 中传播——
  `|lower| = max_card` 时上界收缩为下界；`|upper| = min_card` 时下界提升为上界

传播到不动点后，若仍有未确定变量则做包含/排除二分回溯搜索。

## 独立检查器

`solve()` 返回的解先用 `check_solution(constraints, domains, assignment)`
对**原始约束**（建变量时记录的域界 + 各约束自身的 `check()`）独立验证，
验证失败会抛 `AssertionError` 而不是交出错误解。

## 运行

```sh
python3.11 example.py     # 运行样例
python3.11 -m unittest -v # 22 个测试
```
