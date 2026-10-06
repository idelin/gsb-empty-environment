# material — Go 证据材料库

每项证据带内容摘要、版本与签收人集合；内容改动、签收、撤回、冻结与批量发布的语义如下。

## 核心语义

- **版本演进**：`Create` 产生 v1；`Update` 改动内容摘要产生新版本，**新版本的签收人集合为空**，旧版本及其签收记录完整保留（`evidence.go`）。
- **签收 / 撤回**：`Sign` / `Withdraw` 均按指定版本生效，互不影响其他版本。
- **冻结集合**：`Freeze` 捕获「证据 ID → 当前版本号」快照（`FrozenSet`）。
- **批量发布**：`Publish(frozen, ids...)` 为原子操作——先校验全部成员（版本未推进、签收非空、依赖落在同一冻结集合内），任一失败则整批失败、库内状态零变化（`publish.go`）。
- **版本图**：`Graph` 记录版本节点、依赖边与签收/撤回/发布事件流；`CheckFrozen` 核对依赖不混用新旧版本，`SignerOf` 可重放事件流与库状态交叉核对（`graph.go`）。

## 示例

```go
l := material.NewLibrary()
l.Create("report", "报告")
l.Create("attach", "附件")
l.Sign("report", 1, "alice")
l.Sign("attach", 1, "bob")
l.Graph().AddDependency(material.Ref{"report", 1}, material.Ref{"attach", 1})

frozen, _ := l.Freeze("report", "attach")
// 冻结后若 attach 被 Update 或签收被 Withdraw，下面的 Publish 将整批失败
res, err := l.Publish(frozen, "report", "attach")
```

## 测试

```sh
go test ./...
```
