# S/MIME能力列表来源与降级拒绝

S/MIME 邮件的签名属性里可带 SMIMECapabilities，客户端据此选择回信的加密算法，攻击者可伪造能力列表让对方降级到弱算法。给Go邮件客户端写供解析已验签邮件调用的能力库组件：只有签名验证通过且签名证书邮箱与 From 一致的邮件才能记录能力；同一发送者的更新须有更晚的 signingTime，否则忽略；只记录调用方白名单内的算法，白名单外的忽略；选择算法时取对方能力与本地偏好交集中本地偏好最靠前者，交集为空时失败，不得退回白名单外的弱算法；没有记录时使用调用方配置的基线算法。用未签名邮件、邮箱不符、旧时间戳重放、只含弱算法的能力验证。

## 用法

组件以调用方已完成 CMS 签名验证为前提。`SignatureVerified` 只应在验签成功后置为
`true`，`SignerEmail` 应取自同一张已验证的签名证书。

```go
policy, err := netsec.NewCapabilityStore(netsec.Config{
	AllowedAlgorithms: []netsec.Algorithm{
		netsec.AES256GCM,
		netsec.AES192GCM,
		netsec.AES128GCM,
		netsec.AES256CBC,
	},
	LocalPreferences: []netsec.Algorithm{
		netsec.AES256GCM,
		netsec.AES192GCM,
		netsec.AES128GCM,
		netsec.AES256CBC,
	},
	Baseline: netsec.AES256GCM,
})
if err != nil {
	return err
}

capabilities, _, err := netsec.ExtractSMIMECapabilitiesFromSignedAttributes(signedAttrsDER)
if err != nil {
	return err
}

if _, err := policy.Record(netsec.SignedMessage{
	From:              fromHeader,
	SignatureVerified: true,
	SignerEmail:       signerCertificateEmail,
	SigningTime:       signingTimeFromSignedAttributes,
	Capabilities:      capabilities,
}); err != nil {
	return err
}

replyAlgorithm, err := policy.Select("alice@example.org")
if err != nil {
	return err
}
```

如果解析器已将单个 AttributeValue 单独取出，也可以直接调用
`ParseSMIMECapabilities`，再通过 `Capabilities` 字段交给 `Record`；也可以把单个
AttributeValue 的 DER 直接放入 `SMIMECapabilities`。两者必须来自已验签的
SignedAttributes，不能来自未认证的外层邮件内容。

## 策略

- `Record` 依次检查验签标志、签名证书邮箱与唯一 From 邮箱、非空 signingTime、
  严格更晚的更新时间和白名单过滤。任何一项不满足都不会写入能力记录；即使较新的
  邮件没有可用能力，也会推进该发送者的重放时间窗口。
- 能力列表先整体过滤，再原子替换该发送者的记录；仅含白名单外算法的新邮件不会
  清空或覆盖已有记录。
- `Select` 只使用记录中的能力与 `LocalPreferences` 的交集，按本地偏好顺序返回第一项。
  有记录但交集为空时返回 `ErrNoCompatibleAlgorithm`，不会退回基线或弱算法。
- 没有记录时返回 `Baseline`。`Baseline` 必须位于白名单内，否则
  `NewCapabilityStore` 返回 `ErrInvalidConfig`。
- `LocalPreferences` 中不在白名单内的项会被丢弃；调用方可使用
  `Algorithm.IsWeak` 显式排除 RC2、RC4、DES、3DES、RC5。

## 验证

`go test ./...` 覆盖未签名邮件、签名邮箱与 From 不符、旧/相同 signingTime 重放、
仅含弱算法、白名单过滤、本地偏好优先、交集为空必须失败以及无记录时使用基线。
