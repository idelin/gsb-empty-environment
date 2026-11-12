# 镜像层与证明主体匹配

攻击者复用其他镜像的attestation，或把subject写成层或配置摘要。为Go准入控制器实现可导入的OCI镜像核对包：in-toto subject须含所部署manifest的sha256；config.rootfs.diff_ids须与逐层解压后摘要一一对应，layers压缩摘要同样核对。用换subject、少层、层序互换和伪造gzip样例测试。

## 包：`netsec`（仅依赖 Go 标准库）

### 核对规则

1. **Subject 绑定 manifest**：`VerifyManifestSubject` 对准入请求中的
   manifest 原始字节计算 `sha256`，要求 in-toto statement 的 subject 集合
   中出现同一摘要。重放其他镜像的 attestation、或把 subject 写成层/config
   摘要都会返回 `ErrSubjectMismatch`。摘要按原始字节计算而非重新序列化，
   语义相同但字节不同的 manifest 不能共用 attestation。
2. **Config 核对**：`VerifyConfigBlob` 校验 config 压缩（即存储）摘要与
   manifest descriptor 一致、可选 size 一致，并解析 `rootfs.diff_ids`。
3. **层数一致**：`VerifyLayers` 要求 manifest 层数、提供的 blob 数与
   `rootfs.diff_ids` 长度完全相等，少层即 `ErrLayerCount`。
4. **压缩摘要核对**：每个层 blob 的 `sha256` 必须等于 manifest 中
   descriptor 的摘要（`ErrLayerDigest`），声明了 size 时一并核对
   （`ErrLayerSize`）。
5. **解压后 diff_id 逐位核对**：逐层 gzip 解压（或按 media type 处理
   未压缩 tar），对 tar 内容计算 `sha256`，与同位置的
   `rootfs.diff_ids[i]` 一一对应比较。层序互换返回 `ErrDiffID`，伪造/
   截断的 gzip 流返回 `ErrDecompress`。

所有错误都包装在可导出的哨兵错误上，准入策略可用 `errors.Is` 映射拒绝
原因（`ErrSubjectMismatch`、`ErrLayerCount`、`ErrLayerDigest`、
`ErrDiffID`、`ErrDecompress` 等）。

### 用法

```go
import "example.org/netsec/q0131"

// 已持有全部 blob 时
stmt, err := netsec.ParseStatement(attestationJSON)
if err != nil { /* 拒绝：ErrMalformedJSON / ErrBadDigest */ }

if err := netsec.Verify(manifestJSON, stmt, configBlob, layerBlobs); err != nil {
    admissionDenied(err) // errors.Is 判定具体原因
}

// blob 在 registry / containerd 中时
getter := netsec.BlobGetterFunc(func(ctx context.Context, d netsec.Digest) ([]byte, error) {
    return registry.Fetch(ctx, d.String())
})
err = netsec.VerifyWithBlobs(ctx, manifestJSON, stmt, getter)
```

### 测试

`go test ./...` 使用内存中构造的真实三层 tar/gzip OCI 镜像，覆盖：

- 换 subject（重放 attestation、subject 写成 config/层摘要、仅 sha512、
  重新序列化 manifest）；
- 少层（descriptor 缺失、diff_id 缺失、blob 缺失、getter 取不到）；
- 层序互换（descriptor+blob 同时换序、仅 blob 换序、diff_id 换序）；
- 伪造 gzip（篡改 tar 后重新 gzip、截断流、伪装 gzip 的垃圾、位翻转）；
- 以及 config 篡改、size 不符、错误摘要、JSON 尾随数据、未压缩 tar 层、
  in-toto v0.1/v1 信封等边界。
