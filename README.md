# GlobalPlatform 安全通道降级防护

供 APDU 分发器调用的安全通道状态包。它负责：

- 安全级别单调提升：`C_MAC`（0x01）与 `C_DECRYPTION`（0x02）只能新增，不能回退。
- `INSTALL`（0xE6）、`LOAD`（0xE8）、`DELETE`（0xE4）必须同时具备 C-MAC 与
  C-DECRYPTION。
- 每条 C-MAC 命令使用前一条命令返回的链接值，并严格按顺序消耗计数器。
- MAC 错误、重放、计数器跳过/回退、MAC 回调失败或分发处理失败会永久作废通道。
- MAC 算法与会话密钥不进入状态包，由宿主通过 `MACFunc` 注入。

## 使用

```go
mac := func(counter uint32, chaining, apdu []byte) ([]byte, error) {
	// 使用宿主持有的会话密钥计算 C-MAC，并返回新的链接值。
	return hostComputeCMAC(counter, chaining, apdu)
}

channel := netsec.NewChannel(mac)

// 初始化更新后，将首条 EXTERNAL AUTHENTICATE 交由此方法校验。
err := channel.ExternalAuthenticate(netsec.AuthRequest{
	Level:                netsec.LevelFull,
	InitialChainingValue: initialChaining,
	Counter:              nextCounter,
	APDU:                 externalAuthenticateAPDU,
	MAC:                  externalAuthenticateMAC,
})

// 分发器在调用具体指令处理器前校验 C-MAC 与计数器。
err = channel.Dispatch(counter, commandAPDU, receivedMAC, func() error {
	return dispatchToCard(commandAPDU)
})
```

`ExternalAuthenticate` 在已认证通道上收到较低级别时返回
`ErrSecurityLevelDowngrade`，保留原级别。`VerifyCommand` 成功后才推进链接值与
计数器；MAC 或计数器校验失败时，后续调用均返回 `ErrChannelInvalid`。
