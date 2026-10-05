# permdiff — 权限差异检查器

判断新授权规则是否比旧规则**扩大了权限**。规则建立在有限域上的
整数区间、标签集合与布尔条件之上；若扩大，返回使差异成立的
**最小主体/资源属性组合**（见证）以及双方的**解释树**。

## 核心语义

- **三值逻辑（Kleene）**：每个子表达式求值为 `true / false / unknown`。
  属性缺失（或类型不符）时谓词为 `unknown`，并按 `AND=min, OR=max,
  NOT(unknown)=unknown` 传播，**不按二值逻辑化简**。
- **未知即拒绝**：授权判定 `Decide` 仅当整体为 `true` 时允许；
  `false` 与 `unknown` 都拒绝。
- **扩大定义**：存在赋值 `a` 使得 `new(a) = true` 且 `old(a) ≠ true`
  （即 `false` 或 `unknown`）。注意旧规则因缺失属性得到 `unknown`
  也算被扩大——新规则不再要求该属性即是一种放权。
- **最小见证**：先贪心删除整个属性（退化为缺失），再缩减集合元素，
  按「属性个数 → 集合总大小 → 整数和」取最小。

## 规则 JSON 格式

```json
{"and": [cond, ...]}                      {"or": [cond, ...]}
{"not": cond}
{"intRange":    {"attr": "clearance", "min": 2, "max": 10}}
{"bool":        {"attr": "mfa", "value": true}}
{"subsetOf":    {"attr": "roles", "set": ["dev", "ops"]}}
{"containsAll": {"attr": "roles", "set": ["dev"]}}
{"contains":    {"attr": "roles", "elem": "dev"}}
{"intersects":  {"attr": "roles", "set": ["dev"]}}
```

属性赋值：`{"clearance": 5, "mfa": true, "roles": ["dev"]}`，
未列出的属性即缺失。

## 使用

```sh
# 差异检查：输出是否扩大、最小见证与双方解释树
go run ./cmd/permdiff -old examples/old.json -new examples/new.json -domain 0:10

# 单规则求值：展示三值结果与解释树（unknown 最终拒绝）
go run ./cmd/permdiff -eval examples/old.json -attrs examples/attrs.json

go test ./...
```

## 库 API

```go
res := permdiff.Diff(oldCond, newCond, permdiff.Config{IntMin: 0, IntMax: 10})
// res.Expands / res.Witness / res.NewTree / res.OldTree

allowed, tree := permdiff.Decide(cond, assignment)
```

## 实现说明

- `trival.go`：三值逻辑运算。
- `value.go` / `predicate.go` / `cond.go`：属性值、谓词与条件 AST，
  求值时同步构建解释树（`explain.go`）。
- `diff.go`：候选值生成（整数取区间端点及相邻点；集合取字面集合、
  元素全集的单例/配对及增减变体），在有限域上枚举并贪心最小化见证；
  `Config.MaxEnum` 限制枚举规模，截断时 `Result.Truncated` 为真。
