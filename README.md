# 安全回归反例持久身份

`netsec` 包供 Go 测试宿主在版本演进中追踪同一个安全反例。路径、错误行号
和对象编号都会变化，因此关联不依赖这些易变文本，而依赖三类稳定证据：

- 稳定断言 ID：`Counterexample.AssertionID` 是跨版本不变的关联主键。
- 对象来源图：`Graph` 记录触发断言的节点（函数、对象、断言等）和依赖边。
- 结构化输入：`Input` 以递归字段/序列/叶子值表示，`Input.Equal` 做结构
  相等比较，不保留也不比较原始字符串。

## 关联规则

`Associate` 只接受调用方通过 `NewNodeMigration` + `Map` 显式提供的节点
迁移映射，并给出三种结论：

- `associated`：断言 ID 相同、输入结构相等，且迁移覆盖旧图全部节点并保持
  每条依赖边（`NodeMigration.Check` 逐条在新图中找对应边）。
- `ambiguous`：同一旧节点显式登记了多个新节点候选，候选全部保留，绝不自动
  选边或按名称/文本相似度合并。
- `diverged`：断言 ID 变化、输入结构变化、节点未映射、映射端点不存在或依赖
  边断裂；具体原因通过 `AssociationEvidence.Violations` 暴露。

## 测试宿主用法

```go
tracker := netsec.NewTracker()
_ = tracker.Add(oldCounter) // 旧版本反例
_ = tracker.Add(newCounter) // 新版本反例

migration := netsec.NewNodeMigration("v1", "v2")
_ = migration.Map("f1", "f1a") // 函数拆分后的落点由调用方指认
_ = migration.Map("o7", "o209")

evidence, err := tracker.AssociatePair("v1", "v2", "SEC-SQLI-001", migration)
// evidence.Verdict: associated / ambiguous / diverged
```

## 验证场景

`associate_test.go` 覆盖要求的三类关联证据：

- 函数拆分：旧函数拆成两个新函数且对象编号变化，双候选映射先判
  `ambiguous`，调用方收窄候选后判 `associated`。
- 对象编号变化：`obj-17 -> obj-402` 等纯编号/重命名变化在依赖边保持时
  正常关联；依赖关系被改掉时判 `diverged` 并报 `dependency-broken`。
- 两个相近缺陷：SQLi 与 XSS 反例位置相邻、输入相似，但断言 ID 不同，
  跨缺陷关联一律 `diverged`，各自独立追踪时仍可 `associated`。

运行：`go test ./...`
