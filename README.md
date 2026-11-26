# APK 分块签名准入验证

`apkverify` 直接解析调用方提供的原始 APK 字节切片，不接受任何外部预设的
ZIP 偏移。每个 LFH、CD 条目、EOCD 和 APK Signing Block 位置都由字节内容
自行推导。

```go
minSDK := int32(24)
report, err := apkverify.Validate(apkBytes, apkverify.Options{
    MinSDKVersion: &minSDK,
    Verifier: func(ctx context.Context, req apkverify.VerificationRequest) error {
        // req.Coverage 已严格按 APK Signature Scheme 拼接：
        // section1 || full central directory || normalized EOCD。
        return verifyV2V3V31Signatures(req)
    },
})
```

`Parse` 只做结构校验，可用于观测和测试；`Validate` 才是准入入口。存在
v2、v3 或 v3.1 块时必须提供 `Verifier`，回调必须核对所有已识别签名块的
签名和内容摘要。缺少 v2+ 块时默认拒绝；仅当 `MinSDKVersion < 24` 且显式
设置 `AllowV1Only` 时才允许无 v2+ 的旧式 APK。

结构校验拒绝：

- 首 4 字节不是 ZIP LFH，或首个 LFH 不在偏移 `0`，包括 `dex\n`、ELF 等前缀。
- 任意 ZIP64 EOCD 标识、sentinel、locator 或 `0x0001` extra field。
- EOCD 注释长度与实际缓冲末尾不符、尾部冗余字节、多个 EOCD 候选。
- 重复条目名，以及 LFH/CD 的名称、CRC32、压缩大小、未压缩大小、偏移、
  压缩方法或标志不一致。
- 本地条目区间重叠、未归属间隙、CD 未精确结束于 EOCD。
- Signing Block 前后 8 字节大小不一致、块长/ID-value 对齐错误、重复块 ID。
- v3.1 缺少 v3、v3.1 目标低于 API 33、v3 在 API 33 前截止却无 v3.1 回退。

`VerificationRequest.SigningBlock` 提供 v2/v3/v3.1 signer、目标 SDK 范围和
proof-of-rotation lineage 的结构化结果，供签名回调执行证书链、摘要和轮换
策略校验。
