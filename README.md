# pathpat

路径模式检查器：给定允许模式集合与拒绝模式集合，判断是否存在
“被允许且不被拒绝”的路径，并返回段数最少的见证路径。

## 模式语言

模式只含三种段：

- 字面段：恰好匹配一个相同段；
- `*`：单段通配，恰好匹配一个任意段（**不能跨越分隔符**）；
- `**`：多段通配，匹配零个或多个段。

## 路径模型

- 空路径 `""`：零个段；
- 根路径 `"/"`：恰好一个空段 `[""]`；
- 二者被明确区分：`SplitPath("")` 返回 `nil`，`SplitPath("/")` 返回 `[]string{""}`。

## 判定方法（`Solve`）

在有限字母表上按段数递增穷举候选路径：字母表 = 所有模式中出现过的
字面段 + 一个代表“任意其他段”的新鲜符号。穷举状态记录允许/拒绝 NFA
各自可达的状态子集，相同状态被合并——语义与逐条路径穷举一致，但搜索
空间有限：若所有可达状态耗尽仍未找到见证，则可判定不存在（`None`）。

- 找到见证 → `Witness`，段数最少（BFS 按层推进）；
- 状态空间耗尽 → `None`；
- 预算（`Budget.MaxExpansions` / `Budget.MaxDepth`，非正表示不限）
  不足 → `Unknown`。

`BruteForce` 是逐条枚举短路径的参考实现，用于在小规模用例上交叉验证
`Solve`（见 `solve_test.go` 的随机对拍测试）。

## 示例

```go
allow := []pathpat.Pattern{pathpat.ParsePattern("**")}
deny := []pathpat.Pattern{pathpat.ParsePattern(""), pathpat.ParsePattern("*")}
res := pathpat.Solve(allow, deny, pathpat.Budget{})
// res.Verdict == pathpat.Witness, res.Witness 为 2 段路径
```

## 测试

```sh
go test ./...
```
