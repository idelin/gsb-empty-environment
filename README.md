# material — Go 证据材料库

每项证据（`Evidence`）由若干不可变版本记录（`VersionRecord`）组成，
每条记录包含内容摘要（`Digest`）、版本号和签收人集合（`Signers`）。

## 核心规则

- **内容改动**：`UpdateContent` 产生新版本，新版本签收集合为空；
  旧版本记录（摘要 + 签收人）原样保留，仍可查询。
- **签收**：`Sign` 按精确版本签收；已撤回（`Withdraw`）版本拒绝签收。
- **冻结依赖**：`SetDependencies` 将某版本的依赖钉在精确的 `(证据, 版本)` 上。
- **批量发布**：`Publish` 要求批次构成同一冻结集合——集合内任何版本
  声明的依赖都必须精确落在集合内，禁止混用新旧签收；任一校验失败，
  整个批次原子失败，不产生部分发布状态。

## 版本图核对

`Library.Graph()` 对库做一致快照，提供只读核对：

- `DependencyUpdates()`：检出被钉在旧版、且已有更新版本的依赖边；
- `WithdrawnDeps()`：检出指向已撤回版本的依赖边；
- `VerifyFrozenSet(batch)`：发布前的冻结集合一致性校验（`Publish` 内部即走此路径）。

## 示例

```go
l := material.NewLibrary()
l.AddEvidence("ev-a", "digest-a1")
l.AddEvidence("ev-b", "digest-b1")
l.Sign("ev-a", 1, "alice")
l.Sign("ev-b", 1, "bob")
l.SetDependencies("ev-a", 1, []material.Ref{{EvidenceID: "ev-b", Version: 1}})

err := l.Publish([]material.Ref{{"ev-a", 1}, {"ev-b", 1}}) // 原子提交
```

## 测试

```sh
go test ./...
```
