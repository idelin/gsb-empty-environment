# OCI引用列表投毒过滤

镜像仓库对同一subject返回的referrers列表可被推入伪造签名或证明。为Go验证客户端实现可导入的过滤包：只留artifactType在许可表内的描述符，逐项取回manifest，核对subject确为被查摘要且内容摘要与描述符一致；回退标签sha256-<hex>索引同样处理。用伪subject、被换内容和重复摘要样例测试，被滤除项不得进入验签。

## 使用

包入口是模块根目录的 `netsec` 包。调用方提供按摘要/标签取回 manifest 的
`ManifestFetcher`，过滤包只把通过全部检查的描述符交给验证器：

```go
allowlist := netsec.NewAllowlist(
	"application/vnd.dev.cosign.simplesigning.v1+json",
	"application/vnd.in-toto+json",
)

result, err := netsec.FilterAndVerify(
	ctx,
	subjectDigest,
	referrersFromRegistry,
	allowlist,
	fetcher,
	verifySignatureOrAttestation,
)
```

`result.Accepted` 可直接进入验签；`result.Rejected` 记录被丢弃原因，但被丢弃
的描述符不会传给 `verifySignatureOrAttestation`。注册表不支持 referrers API
时，用 `FilterFallback` 读取 `sha256-<hex>` 标签索引，或使用
`FilterReferrersWithFallback` 自动回退。`FilterSources` 可同时处理 referrers
响应和回退索引，并按摘要去重。

过滤条件：

- 描述符的 `artifactType` 必须在许可表内；描述符未提供类型时，以摘要校验后的
  manifest 中 `artifactType` 为准。
- manifest 内容的摘要必须与描述符 `digest` 一致。
- manifest 的 `subject.digest` 必须与被查询的 subject 完全一致。
- 描述符与 manifest 都声明类型时，两者必须一致。
- 相同摘要只验签一次，跨 referrers 响应与回退索引也会去重。
