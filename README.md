# 早期导出与主导出

早期导出密钥和握手后的导出密钥被当成可以互换。两种导出使用不同的秘密；调用方必须标明阶段，阶段不符的输出不能拿去验证另一阶段。同一阶段重复导出得到相同字节。可导入的Go库供TLS栈调用。用早期秘密验证握手后导出的标签。

## API

`PhaseEarly`（`e exp master`）和 `PhasePostHandshake`（`exp master`）是两个
独立的导出阶段。`EarlyExporterMasterSecret` 与 `ExporterMasterSecret` 是不同
的类型，能在编译期阻止把两个阶段的秘密混用。

```go
es := netsec.NewEarlySecret(sha256.New, psk)
early, err := es.EarlyExporterMasterSecret(earlyTranscript).Export("label", nil, 32)
if err != nil {
	// handle error
}

// 重复导出是确定性的；阶段和秘密不会在导出过程中改变。
again, _ := es.EarlyExporterMasterSecret(earlyTranscript).Export("label", nil, 32)
// bytes.Equal(early, again) == true
```

需要直接由秘密导出的调用方可以使用
`ExportKeyingMaterial(phase, secret, transcript, label, context, length)`；如果
给的是已经派生好的阶段导出主秘密，则省略 transcript。验证时使用
`VerifyKeyingMaterial(phase, secret, label, context, expected)`，它会按给定阶段
重新导出并做常量时间比较。用早期秘密验证握手后导出的字节会失败。
