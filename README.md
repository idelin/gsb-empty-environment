# TLS证书压缩资源隔离

## 组件

- `decompressor.go`：`Decompressor` 工厂与单次使用的 `StepDecompressor` 可计步解压器接口，具体压缩算法（zlib/brotli/zstd）由宿主按协商结果注入。
- `certdecompress.go`：握手宿主调用的 `Wrapper` 与 `Decompress` 入口。
- `certdecompress_test.go`：脚本化可计步解压器，覆盖错长度、尾随压缩帧、取消、步数/输出上限等路径。

## 安全边界

`Wrapper.Decompress` 在调用证书解析回调前依次保证：

1. 算法 id 必须在构造 `Wrapper` 时传入的协商集合内，否则返回 `ErrAlgorithmNotNegotiated`。
2. 对端声明的解压长度必须为正且不超过 `Limits.MaxDeclaredLength`。
3. 实际输出不得超过 `Limits.MaxOutput`，与声明长度不一致返回 `ErrLengthMismatch`。
4. 总工作步数不得超过 `Limits.MaxSteps`；空转的步骤以 `ErrNoProgress` 立即终止。
5. 流结束后若仍有未消费压缩字节（如拼接的第二帧）返回 `ErrTrailingData`。
6. `context.Context` 取消在每一步前检查，取消后不会产生任何回调。

只有全部检查通过、证书链完整解压后才调用一次 `ParseCertChain`；任何失败路径都不会把半成品缓冲区交给回调。`Wrapper` 只读不可变配置，可被多个握手并发使用。

## 宿主接入

```go
wrapper, err := netsec.NewWrapper(netsec.Limits{
    MaxDeclaredLength: 1 << 16,
    MaxOutput:         1 << 16,
    MaxSteps:          1 << 20,
}, zlibDecompressor, zstdDecompressor) // 仅传入本次握手协商过的算法

err = wrapper.Decompress(ctx, peerAlgorithm, peerDeclaredLength, compressed,
    func(algorithm uint16, chain []byte) error {
        return parseCertificates(chain) // 仅在此处看到完整证书链
    })
```

接入端解压证书链时，压缩长度很小也可能耗尽资源。写供Go握手宿主调用的证书解压包装器，算法仅可来自协商集合，声明长度、实际输出和总工作步数均设上限，完整解压后才交给证书解析回调。通过可计步解压器测试错长度、尾随压缩帧及取消，不暴露半份证书链。
