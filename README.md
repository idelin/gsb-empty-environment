# 源优先级固定的 Go 模块解析组件

本包（`example.org/netsec/q0338`）用于 Go 依赖解析服务。它先根据模块路径所属命名空间锁定唯一来源，再访问该来源的仓库；不会先探测公共源，也不会因公共源存在同名包而切换来源。

## 关键不变量

- 命名空间在 `NewPolicy` 时冻结，每个模块路径只匹配一个来源。显式命名空间重叠且指向不同来源会直接拒绝策略；`*` 只作未匹配时的默认项。
- 来源声明、命名空间绑定和同源镜像顺序均按配置声明顺序保存，访问器返回副本，包内 `source`、`repository`、`checksum` 等元数据不会参与选源。
- 解析要求精确版本。仓库返回的 `SourceID`、模块路径和版本必须与声明一致，否则分别按来源冲突、非法制品或版本不符拒绝。
- 下载内容会重新计算 Go `h1` 校验和（也支持 `sha256:`）；请求可附 `ExpectedChecksum` 锁定值。缓存命中同样重新验证，不信任缓存内容。
- 默认 `SignaturesRequired`，Ed25519 签名覆盖来源、模块、版本和校验和；签名密钥必须在可信 allowlist 中。仅允许显式设置 `SignaturesIfPresent` 或 `SignaturesIgnored` 放宽。
- 代理缓存以 `SourceID + module + version` 物理分区，同名同版本在不同来源各占一份。
- 运输失败只允许在同一来源的固定顺序镜像间回退，并记录 `fallback` 和 `degraded`；同源仓库耗尽后记录 `fallback_blocked` 并失败，绝不会跨来源回退。
- `AuditEvent` 同时进入结果和可注入的 `Auditor`，覆盖选源、缓存命中/未命中、仓库失败、回退、降级、冲突和拒绝。

## 用法

```go
policy, err := netsec.NewPolicy(netsec.PolicyConfig{
	Sources: []netsec.Source{
		{ID: "public-proxy", Kind: netsec.SourcePublic},
		{ID: "corp-private", Kind: netsec.SourcePrivate},
	},
	Bindings: []netsec.NamespaceBinding{
		{Namespace: "corp.example/internal", SourceID: "corp-private"},
		{Namespace: "*", SourceID: "public-proxy"},
	},
})
if err != nil {
	return err
}

resolver, err := netsec.NewResolver(netsec.ResolverConfig{
	Policy: policy,
	Repositories: []netsec.RepositoryBinding{
		{SourceID: "public-proxy", Repository: publicRepository},
		{SourceID: "corp-private", Repository: privatePrimary},
		// 同一 SourceID 的后续绑定才是受控的同源镜像。
		{SourceID: "corp-private", Repository: privateMirror},
	},
	Verifier: netsec.NewEd25519Verifier(trustedKeys),
	Auditor:  auditSink,
})
if err != nil {
	return err
}

resolution, err := resolver.Resolve(ctx, netsec.Request{
	ModulePath:        "corp.example/internal/payments",
	Version:           "v1.2.3",
	ExpectedChecksum:  "h1:...", // 来自受信 lockfile/sumdb
	ExpectedSignerKeyID: "corp-release-key",
})
```

`Repository.Fetch` 返回模块 zip 内容、其校验和、来源标识及签名。`Metadata` 仅供业务展示，解析器不会从中读取来源、版本、校验和或签名。

## 验证

`go test ./...` 包含同名公共包不被访问、包元数据改源无效、校验和/签名/版本失败拒绝、同一模块跨源冲突、合法私有包解析、缓存命中、缓存源分区、同源镜像回退审计，以及跨源回退被阻止等用例。
