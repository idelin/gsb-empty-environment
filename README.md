# entitygroup

Go 实体分组库：以显式 **必须同组**（must-together）与 **不得同组**（must-not-together）
关系维护实体分组，基于图连通模型做冲突核对、路径证据与事务回滚。

## 模型

- **必须同组**：等价关系，传递闭包形成分组（无向图连通分量）。
- **不得同组**：约束边，禁止两端落入同一分组。
- **相似度建议**：`SuggestSimilar` 仅记录软建议，绝不自动形成硬关系。

## 冲突核对与路径证据

每次提交关系前先做连通性核对，冲突则拒绝本次关系、Store 保持原状，
并返回 `*ConflictError` 携带证据：

- 新增 must-together(a, b)：若合并 comp(a) ∪ comp(b) 后某条禁止边 (x, y)
  两端同组，则拒绝；`Path` 为 `x ~ a — b ~ y` 的完整同组路径，
  `ForbiddenEdge` 为被撞上的禁止边。
- 新增 must-not-together(a, b)：若 a、b 在现有闭包中已连通，则拒绝；
  `Path` 为 a 到 b 的现有同组路径。

## 事务与撤销

每条成功提交的关系记入事务日志；`Undo()` 弹出最近一条并回滚图状态，
返回被撤销关系的描述。重复提交与空操作不入日志；被拒绝的关系不入日志。

## 示例

```go
s := entitygroup.New()
s.AddMustTogether("a", "b")
s.AddMustTogether("b", "c")
s.AddMustNotTogether("a", "d")

err := s.AddMustTogether("c", "d") // 拒绝：闭包撞上禁止边 a-d
if ce, ok := err.(*entitygroup.ConflictError); ok {
    // ce.Path == [d a b c]（或反向），ce.ForbiddenEdge == {"a","d"}
}

s.SuggestSimilar("c", "e", 0.91)   // 仅建议，不分组
s.Undo()                            // 撤销 must-not-together(a, d)
s.AddMustTogether("c", "d")         // 现在可以成功
```

## 测试

```sh
GO111MODULE=on go test ./...
```
