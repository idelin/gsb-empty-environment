# policydiff — 权限差异检查器

在有限域上比较新旧两条授权规则，判断**新规则是否扩大授权**；
若扩大，返回**最小的主体/资源属性组合**（反例）以及新旧规则的**解释树**。

## 语义

- 条件类型：整数范围 `attr ∈ [lo, hi]`、标签集合 `attr ∈ {...}`、布尔条件 `attr == v`，
  以及 `AND` / `OR` / `NOT` 组合。
- **三值逻辑（Kleene 强逻辑）**：请求缺失属性时，相关条件求值为 `Unknown`，
  不能按普通二值逻辑化简（例如 `False AND Unknown = False`，`True OR Unknown = True`）。
- **deny-by-default**：仅当规则整体求值为 `True` 才放行；`Unknown` 与 `False` 一律拒绝。
- **扩大授权**：存在某个请求环境（部分属性赋值），新规则求值为 `True` 而旧规则非 `True`。
- **最小反例**：已赋值属性个数最少；同数量时按属性名与取值域顺序取最先者。

## 用法

```sh
go run ./cmd/policydiff -schema examples/schema.json -old examples/old.json -new examples/new.json
```

### 模式（schema.json）

```json
{"attributes": {
  "subject.level":  {"type": "int", "min": 0, "max": 10},
  "resource.label": {"type": "label", "values": ["public", "internal", "secret"]},
  "subject.mfa":    {"type": "bool"}
}}
```

### 规则（old.json / new.json）

```json
{"and": [
  {"intRange": {"attr": "subject.level", "lo": 8, "hi": 10}},
  {"or": [
    {"labelIn": {"attr": "resource.label", "set": ["public", "internal"]}},
    {"bool": {"attr": "subject.mfa", "value": true}}
  ]},
  {"not": {"labelIn": {"attr": "resource.label", "set": ["secret"]}}}
]}
```

## 库 API

```go
schema := policydiff.Schema{"subject.level": {Type: "int", Min: 0, Max: 10}}
oldRule := policydiff.IntRange("subject.level", 8, 10)
newRule := policydiff.IntRange("subject.level", 5, 10)
res, _ := policydiff.CheckExpansion(schema, oldRule, newRule)
// res.Expands == true；res.Counterexample == {subject.level: 5}
// res.NewTree / res.OldTree 为解释树，可 String() 渲染
```

## 算法

所有域均为有限域，检查通过枚举完成，并利用三值逻辑关于信息量的单调性剪枝：
部分赋值下若新规则已为 `False`（不可能再变 `True`）或旧规则已为 `True`
（不可能再被拒绝），其任何超集都不可能是反例。外层按已赋值属性个数
迭代加深（k = 0..n），保证首个反例即最小反例。

## 测试

```sh
go test ./...
```
