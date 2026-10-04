# invcheck — 循环不变量检查器

对整数仿射循环（线性守卫 + 仿射并行赋值）在**有限整数域**上穷举验证候选不变量。
候选不变量、初始条件、后置条件均由输入提供。

## 检查内容

1. **初始成立（initiation）**：`init ⇒ invariant`
2. **一步保持（preservation）**：`invariant ∧ guard ⇒ invariant(body(s))`，赋值按并行语义（右端全部取旧值）
3. **充分性（sufficiency）**：`invariant ∧ ¬guard ⇒ post`

所有线性表达式用任意精度整数求值，不存在中间溢出。

## 判定

- `valid`：三项检查在域内全部通过；
- `invalid`：某项失败，报告中给出具体反例状态（`init`/`sufficiency`）或具体转移 `from → to`（`preservation`）；
- `unknown`：域内检查均通过，但存在**越界转移**（后继状态超出声明的有限范围）。
  越界转移在 `preservation.outOfBounds` 中单独列出，**不当作证明成立**。

## 输入格式（JSON）

```json
{
  "variables": ["x"],
  "bounds": {"x": [0, 10]},
  "init":     [{"expr": {"coeffs": {"x": 1}, "const": 0}, "op": "=="}],
  "guard":    [{"expr": {"coeffs": {"x": -1}, "const": 10}, "op": ">"}],
  "body":     [{"var": "x", "expr": {"coeffs": {"x": 1}, "const": 1}}],
  "invariant":[{"expr": {"coeffs": {"x": -1}, "const": 10}, "op": ">="}],
  "post":     [{"expr": {"coeffs": {"x": 1}, "const": -10}, "op": "=="}]
}
```

- 约束含义为 `sum(coeffs[v]*v) + const <op> 0`，`op ∈ {>=, <=, >, <, ==, !=}`，约束列表为合取；
- `body` 为并行赋值列表，每个变量至多赋值一次，未赋值的变量保持不变；
- `bounds` 为每个变量声明闭区间 `[lo, hi]`，状态空间大小受 `-max-states` 限制（默认 5,000,000）。

## 使用

```sh
go build -o invcheck .
./invcheck examples/valid.json            # 或 cat input.json | ./invcheck
```

退出码：`0` = valid，`1` = invalid，`2` = unknown，`3` = 输入/用法错误。

## 测试

```sh
go test ./...
```
