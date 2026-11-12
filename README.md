# Zigbee安装码入网校验

攻击者用公开的默认全局链路密钥或抄错的安装码尝试加入Zigbee网络。给Go信任中心写供入网请求调用的安装码包：安装码长度只能为6、8、12或16字节，后接CRC-16/X-25（小端）；按EUI64登记，用调用方的哈希函数导出链路密钥；已登记设备禁用默认全局密钥；同一EUI64连续错误超过调用方次数后锁定。用错误CRC、默认密钥入网和锁定验证。

## 使用

```go
rawInstallCode, err := netsec.EncodeInstallCode(installCodePayload)
if err != nil {
	// 长度必须是 6、8、12 或 16 字节
}

tc := netsec.NewTrustCenter(
	func(code []byte) []byte {
		// 调用方提供安装码到 16 字节链路密钥的哈希/派生函数
		return derivedLinkKey
	},
	2, // 同一 EUI64 连续失败达到此值后锁定
)

if err := tc.Register(eui64, rawInstallCode); err != nil {
	// 长度、CRC、哈希或默认密钥策略错误
}

linkKey, err := tc.Join(eui64, rawInstallCode, presentedLinkKey)
```

`Join(eui64, installCode)` 会返回登记时由安装码导出的链路密钥。传入第三个参数时，还会用常量时间比较校验请求携带的链路密钥。损坏 CRC、默认全局密钥、安装码或链路密钥不匹配都会计入同一 EUI64 的连续失败次数；成功入网会清零，达到上限后必须由管理员调用 `Reset`/`Unlock`。
