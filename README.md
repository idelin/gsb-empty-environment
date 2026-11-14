# 后续作业取到了被替换的构建产物

流水线用同名产物在作业间传递，攻击者从较低权限作业上传同名产物覆盖原文件，后续发布作业取到被篡改内容。请实现供Go流水线服务调用的可导入产物传递组件。产物按内容摘要寻址并绑定来源作业、运行、提交与作用域，同名不同摘要视为冲突；下载时核对摘要并记录来源链。过期、覆盖与跨工作区访问受限。用同名覆盖、摘要不符、跨运行读取、合法传递和过期验证后续作业只取原始产物。

## 用法

```go
store, err := netsec.NewStore("/var/lib/pipeline/artifacts")
if err != nil {
	return err
}

builder := netsec.Source{
	Workspace: "team-a",
	Scope:     "release",
	RunID:     "run-42",
	JobID:     "build",
	Commit:    "3f6c0d2",
}
artifact, err := store.UploadBytes(ctx, builder, "app.tgz", buildBytes)
if err != nil {
	return err
}

publisher := builder
publisher.JobID = "publish"
data, artifact, err := store.DownloadBytes(ctx, publisher, "app.tgz")
if err != nil {
	return err
}
```

`Upload`/`UploadBytes`、`Download`/`DownloadBytes`/`Fetch`、`Open` 和
`Provenance` 是主要入口；`Put`、`Publish`、`Get`、`Read` 为等价别名。
按内容摘要读取可使用 `FindByDigest`、`DownloadDigest`、`FetchDigest` 和
`OpenDigest`。

## 安全属性

- **内容寻址**：blob 以 SHA-256 摘要命名。下载先完整校验长度和摘要，校验通过后才向调用方写字节；磁盘 blob 被替换时返回 `ErrDigestMismatch`，不会泄漏被篡改内容。
- **不可变名称绑定**：名称在 `(workspace, scope, run)` 命名空间内只绑定一次。同名同摘要上传是幂等的；同名不同摘要返回 `ErrConflict`，原记录、原 blob 和来源绑定均保持不变。
- **来源链**：每个 artifact 记录来源 workspace/scope/run/job/commit。上传、下载、被拒绝的覆盖和完整性失败事件按顺序保存在 manifest 中，可通过 `Provenance`/`Chain` 查询。
- **访问边界**：读取必须来自相同 workspace、scope 和 run，跨工作区返回 `ErrCrossWorkspace`，跨运行返回 `ErrCrossRun`；作业可以不同，以支持同一运行内的下游传递。
- **过期**：上传时可设置 `WithTTL` 或 `WithExpiresAt`，也可在 `NewStore` 上设置默认 TTL。过期后读取、查询和按摘要查找均返回 `ErrExpired`；名称绑定保留为 tombstone，防止过期后同名接管。
- **并发**：同名并发上传只有摘要最先落库者成功，其余返回冲突。内容相同则复用同一 blob。

所有错误均可通过 `errors.Is` 判断，例如 `ErrConflict`、`ErrDigestMismatch`、
`ErrExpired`、`ErrCrossRun` 和 `ErrCrossWorkspace`。

运行验证：

```sh
go test ./...
go test -race ./...
```
