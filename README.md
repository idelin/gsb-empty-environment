# Cosmos 交易解码与 Amino JSON 准入校验

`netsec` 提供无第三方依赖的 Cosmos protobuf 交易解码和 ante 准入校验。它用于阻止
`SIGN_MODE_LEGACY_AMINO_JSON` 硬件钱包显示内容与实际执行内容不一致的交易。

## 校验规则

- `TxBody.extension_options`（字段 1023）非空时，任何签名模式一律拒绝。
- 未识别字段按字段编号分类：小于 1024 为关键字段，出现即拒绝；至少 1024 为非关键字段，
  仅在 `SIGN_MODE_DIRECT` 下允许忽略，其余签名模式（包括
  `SIGN_MODE_LEGACY_AMINO_JSON`）一律拒绝。
- `SIGN_MODE_LEGACY_AMINO_JSON` 只允许调用方 Amino 注册表中存在 amino 名称的消息类型。
- 当区块高度达到调用方配置的弃用高度后，`SIGN_MODE_LEGACY_AMINO_JSON` 只允许白名单地址
  作为签名者使用。高度低于弃用高度时不启用白名单限制。
- `non_critical_extension_options`（字段 2047）也仅能在 `SIGN_MODE_DIRECT` 下使用；其他
  签名模式会被拒绝，避免 Amino 签名文档无法表示的内容被执行。

## Ante 集成

```go
policy := netsec.Policy{
    AminoRegistry: map[string]string{
        "/cosmos.bank.v1beta1.MsgSend": "cosmos-sdk/MsgSend",
    },
    LegacyAminoJSONDeprecationHeight: 123456,
    LegacyAminoJSONWhitelist:         []string{"cosmos1ledger..."},
}

err := policy.AnteHandle(decodedTx, netsec.Request{
    Height:  uint64(ctx.BlockHeight()),
    Signers: txSigners, // ante 中已经解析出的签名者地址
})
```

解码入口：

```go
tx, err := netsec.DecodeTx(raw, netsec.WithPolicy(policy),
    netsec.WithHeight(uint64(ctx.BlockHeight())), netsec.WithSigners(signers...))
```

`DecodeTx` 在未提供策略时按失败关闭处理：没有 Amino 注册表，因此任何包含消息的
`SIGN_MODE_LEGACY_AMINO_JSON` 交易都会被拒绝。若只需要查看原始解码结果而不执行准入，
可调用 `ParseTx`。

`Request.Signers` 应由调用方从已解码/已验证的交易中提供。弃用高度生效后，如果无法取得
签名者地址，校验会失败关闭（返回包装了 `ErrNoLegacyAminoSigner` 的错误）。

## 错误

所有拒绝原因都有可 `errors.Is` 判断的 sentinel：

- `ErrMalformedTx`
- `ErrExtensionOptions`
- `ErrCriticalUnknownField`
- `ErrNonCriticalUnknownField`
- `ErrNonCriticalExtensionOptions`
- `ErrAminoMessageNotRegistered`
- `ErrLegacyAminoJSONDeprecated`
- `ErrNoLegacyAminoSigner`

## 测试

```sh
go test ./...
```

测试覆盖隐藏非关键字段及 1024 边界、任意嵌套 Any 中的未识别字段、未注册消息、
弃用高度前一块/弃用高度/弃用高度后、白名单与非白名单签名者，以及畸形 protobuf。
