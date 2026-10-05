# bpmigrate — 断点迁移工具

文件编辑后，把旧文件中的断点迁移到新文件中的对应语句。工具以**语句结构
和邻域**（前后语句、外层块头、嵌套深度）定位断点，而不是只匹配行文本，
因此复制出的相同代码不会被随意选中。

## 工作原理

1. **语句解析**：把源文件解析为语句序列，每条语句附带结构邻域
   （前一条/后一条语句、最近外层块头、花括号嵌套深度）。
2. **候选匹配**：对新文件中每条语句计算置信度 =
   `0.45*文本相似度 + 0.25*token 相似度 + 0.30*邻域相似度`。
3. **置信冲突仲裁**：
   - 第一名领先第二名超过 `uniqueMargin`(0.12) → 唯一匹配（`moved`/`unchanged`）；
   - 多个候选置信度接近 → `ambiguous`，返回候选列表及各自理由，不随意选择；
   - 最佳候选低于 `deleteThreshold`(0.45) → `deleted`，附最接近的候选供人工确认；
   - 单行无匹配但原语句 token 序列连续分布在多行 → `split`，返回行范围。

## 使用

```sh
go run ./cmd/bpmigrate -old old.go -new new.go -bp 12,27,40
go run ./cmd/bpmigrate -old old.go -new new.go -bp 12 -json
```

## 库 API

```go
results := bpmigrate.Migrate(oldSrc, newSrc, []bpmigrate.Breakpoint{{ID: 1, Line: 12}})
```

每个 `Result` 包含状态（`unchanged`/`moved`/`split`/`deleted`/`ambiguous`）、
唯一行号或候选列表，以及人类可读的判定理由。

## 测试

```sh
go test ./...
```

覆盖：语句移动、拆分、删除、相同行文本（邻域可区分时唯一命中、
整段复制无法区分时返回候选列表）、语句修改后的模糊匹配。
