# 多架构镜像验证与平台选择

`example.org/netsec/q0233` 是一个可导入的 Go 组件，用于从 OCI Image Index 或 Docker Manifest List 中选择平台，并验证完整的摘要链：

1. 标签只解析为根摘要，根清单列表的字节按该摘要验证。
2. 平台匹配使用 `os`、`architecture`、`variant` 和 `os.version`。
3. 选中子清单的字节必须匹配索引中的 descriptor 摘要和长度。
4. 配置和每个层 blob 必须匹配各自 descriptor 的摘要和长度。
5. 缓存只保存已通过验证、按摘要寻址的内容。

## 使用

```go
source := netsec.NewMemorySource() // 生产环境实现 BlobSource / TagSource
resolver := netsec.NewResolver(source)

image, err := resolver.Resolve(ctx, "registry.example/app:latest", netsec.Platform{
    OS:           "linux",
    Architecture: "arm64",
    Variant:      "v8",
})
if err != nil {
    return err
}

for _, layer := range image.Layers {
    // layer.Content 已按 Manifest.Layers[i].Digest 验证。
    reader := layer.Reader()
    _ = reader
}
```

运行时接入只需实现两个窄接口：

```go
type BlobSource interface {
    FetchBlob(ctx context.Context, digest string) ([]byte, error)
}

type TagSource interface {
    ResolveTag(ctx context.Context, reference string) (string, error)
}
```

`NewResolver(source)` 会在 `source` 同时实现 `TagSource` 时自动使用它；也可以显式传入：`NewResolver(blobs, tags)`。

## 安全语义

- 根标签在每次 `Resolve` 时重新解析，标签从不进入缓存。标签移动后新请求得到新根摘要；旧 `ResolvedImage` 保持其已验证内容不变。
- 所有 descriptor 使用声明中的算法重新计算摘要。默认要求整条链使用同一算法；混用会返回 `ErrDigestAlgorithmMismatch`。若确需兼容，可显式使用 `ResolverOptions{AllowMixedDigestAlgorithms: true}`。
- `annotations` 不参与摘要校验、平台匹配或选择。它们仅可在标准 attestation 标记下用于排除不可运行的证明清单。
- 平台 `unknown/unknown` 条目、`artifactType` 清单以及带 `subject` 的 referrer（签名/证明）不会作为可运行镜像被选择。
- 重复平台（包括 `arm64`/`arm64/v8` 等规范化后重复）返回 `ErrDuplicatePlatform`。请求未指定 OS 版本而多个版本匹配时返回 `ErrAmbiguousPlatform`。
- 缺层返回 `ErrMissingLayer` 和 `ErrBlobNotFound`；层内容损坏还返回 `ErrDigestMismatch`。配置与清单层数不一致返回 `ErrLayerCountMismatch`。
- 平台别名为 `x86_64`/`amd64`、`aarch64`/`arm64`，`arm64/v8` 与 `arm64` 等价；其他 variant 必须精确匹配。

错误均可用 `errors.Is` 判断。`MemorySource` 仅用于测试和示例；它允许在固定摘要键下写入损坏内容，以便测试解析器会拒绝篡改。

## 验证

`go test -race ./...` 覆盖合法多架构选择、标签重推、子清单摘要不符、变体混淆、层篡改、缺层、算法混用、重复平台、attestation 排除和缓存命中。
