# invcheck — 有界循环不变量检查器

对整数仿射循环（线性守卫 + 仿射赋值）检查候选不变量。整数域被限定在
有限范围内，检查器在该范围内**穷举所有状态**，判定三个验证条件：

1. **初始成立**：`init ⟹ invariant`
2. **一步保持**：`invariant ∧ guard ⟹ invariant'`（赋值为并行语义，右端在前态求值）
3. **推出后置**：`invariant ∧ ¬guard ⟹ post`

## 判定结果

- `PROVED`：三个条件全部成立，且没有任何转移越出有限范围。
- `FAILED`：存在范围内反例。报告给出具体状态（初始/后置条件失败）
  或具体转移 `pre -> post`（保持性失败）。
- `INDETERMINATE`：范围内没有反例，但存在**越界转移**（后继状态超出
  有限范围）。越界转移单独列出于 `outOfBounds`，**不能当作证明成立**——
  真实执行可能走出已建模的范围，故有界检查不构成证明。

退出码：`0 = PROVED`，`1 = FAILED`，`2 = INDETERMINATE`，`3 = 输入错误`。

## 输入格式（JSON）

```json
{
  "variables": ["x", "y"],
  "bounds": {"min": -20, "max": 20, "vars": {"x": {"min": 0, "max": 10}}},
  "init":      [{"coeffs": {"x": 1}, "op": "==", "const": 0}],
  "guard":     [{"coeffs": {"x": 1}, "op": "<",  "const": 10}],
  "assign":    [{"var": "x", "coeffs": {"x": 1}, "const": 1}],
  "invariant": [{"coeffs": {"x": 1}, "op": "<=", "const": 10}],
  "post":      [{"coeffs": {"x": 1}, "op": ">=", "const": 10}]
}
```

- 约束含义：`sum(coeffs[v] * v) op const`，`op ∈ {<=, <, >=, >, ==, !=}`。
- `init` / `guard` / `invariant` / `post` 均为约束的合取。
- `assign` 为并行仿射赋值；未赋值的变量保持不变。
- `bounds.vars` 可按变量覆盖全局范围（闭区间）。
- 算术使用 `int64`；请让范围与系数保持在不会溢出的规模。

## 使用

```sh
go build -o invcheck .
./invcheck examples/proved.json          # 或 cat input.json | ./invcheck
./invcheck -max-states 1000000 input.json
go test ./...
```

状态空间超过 `-max-states`（默认 10^7）时拒绝执行并报错。
每项检查最多保留 5 个反例样本，完整数量见 `violations` /
`outOfBoundsCount` 字段。

示例见 `examples/`：`proved.json`（可证）、`fail_preservation.json`
（保持性失败，给出反例转移）、`out_of_bounds.json`（越界转移，
判定为 INDETERMINATE）。
