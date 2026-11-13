# 证书压缩输出上限

压缩证书解压后远超声明的长度。解压结果不得超过声明长度和绝对上限；超限或算法标识未知则失败。解压长度与声明一致的证书继续做路径验证。TLS栈调用可导入的Go解码器做这次裁决。声明很小的长度但压缩内容解开会变大。

## 解码器

`netsec` 包实现 RFC 8879 `CompressedCertificate` 消息的解码裁决，仅依赖
标准库（`compress/zlib`）。

```go
decoded, err := netsec.DecodeCompressedCertificate(wire)
if err != nil {
    // 握手失败：用 errors.Is 区分具体原因
}
// decoded.Certificates 为 DER 证书，交由后续路径验证
```

裁决规则：

- 算法标识不是 zlib(1) 时返回 `ErrUnknownAlgorithm`（brotli/zstd 未实现即视为未知）。
- 解压按 4 KiB 分块进行，输出永不超过声明的 `uncompressed_length`，也永不
  超过 `MaxUncompressedCertificate`（1 MiB）绝对上限；越界立即失败且不会
  按对端声明长度分配内存。
- 实际输出大于声明长度返回 `ErrDeclaredLengthExceeded`，小于返回
  `ErrLengthMismatch`，触及绝对上限返回 `ErrAbsoluteLimitExceeded`。
- 只有解压长度与声明完全一致时，才解析内部 TLS 1.3 Certificate 消息
  （请求上下文、证书链、扩展），成功后才进入路径验证。
- 外层消息截断/带尾随字节返回 `ErrTruncated`，zlib 流损坏或校验和失败
  返回 `ErrDecompression`，内部 Certificate 消息畸形返回
  `ErrMalformedCertificate`。

验证：`go test ./...`（含压缩炸弹、绝对上限、未知算法、截断与校验和用例）。
