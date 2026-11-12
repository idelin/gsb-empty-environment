# SEV注入包测量绑定

客户机所有者要在远端确认 AMD SEV 客户机的启动测量后，再注入磁盘密钥，被劫持的宿主机会把同一份注入包重放到调试模式的客户机或别的实例。为Go密钥分发服务写供注入前调用的放行组件：测量须等于预期固件与策略字推导值，策略字不得含调试位，随机数新鲜且只用一次；注入包的 MAC 覆盖客户机标识、测量与随机数，任一不符不得产生密文。用调试位、重放同随机数、换实例标识和测量改动验证，失败时不留下可复用的包。

## 放行边界

`Gate` 只为一个固定的客户机启动状态签发注入包，并在签发和兑换两端执行以下检查：

- 客户机标识必须与配置中的实例一致。
- 策略字不得包含 AMD SEV 的 DEBUG 位（bit 19，`PolicyDebugBit`）。
- 测量必须等于 `ExpectedMeasurement(firmwareDigest, policy)`。
- 随机数长度至少 16 字节、非全零，并且签发和兑换各只能使用一次；可用
  `netsec.GenerateNonce()` 生成 32 字节随机数。
- 包 MAC（HMAC-SHA256）覆盖版本、客户机标识、测量、策略字、随机数和密文。
- 磁盘密钥仅在全部检查及 MAC 验证通过后，才用 AES-256-GCM 包装。
- 任一检查失败时 `Release` 返回零值包，`Ciphertext` 和 `MAC` 均为空；`Open` 不返回任何明文。

测量推导值为：

```text
SHA-256("q0168/sev-launch-measurement/v1" || LE32(len(fw)) || fw || LE64(policy))
```

其中 `fw` 是运维方独立获得的固件测量摘要，`policy` 是 AMD SEV 64 位客户机策略字。

## 用法

```go
cfg := netsec.Config{
    GuestID:        "guest-a",
    FirmwareDigest: expectedFirmwareDigest,
    Policy:         0x00030000,
    MACKey:         macKey,  // 独立随机密钥，至少 16 字节
    WrapKey:        wrapKey, // 独立随机密钥，至少 16 字节
    // 生产多副本/重启场景应提供持久化 NonceStore。
}

gate, err := netsec.NewGate(cfg)
if err != nil {
    return err
}

measurement := netsec.ExpectedMeasurement(cfg.FirmwareDigest, cfg.Policy)
freshNonce, err := netsec.GenerateNonce()
if err != nil {
    return err
}
pkg, err := gate.Release(netsec.ReleaseRequest{
    GuestID:     cfg.GuestID,
    Measurement: measurement,
    Policy:      cfg.Policy,
    Nonce:       freshNonce,
    DiskKey:     diskKey,
})
if err != nil {
    // 失败时 pkg 为零值，没有可注入的密文。
    return err
}
```

宿主把 `pkg` 交给目标客户机时，客户机侧应先用自己的实际启动状态调用
`gate.OpenAttested(pkg, destination)`；它会再次校验包、客户机标识、测量、
策略和随机数，并在成功兑换后返回磁盘密钥。仅调用 `gate.Verify(pkg)` 不会
消耗随机数，可用于注入前预检。

## 重放状态

默认 `NonceStore` 是进程内内存实现。单机组件可直接使用；多副本、重启后仍要
防止重放的服务必须注入一个原子且持久化的 `NonceStore` 实现。`Claim` 成功时
表示 `namespace+nonce` 首次出现。

## 验证

`go test ./...` 覆盖正常签发/兑换、DEBUG 位、同随机数重放、换客户机标识、
测量改动、策略改动、MAC/密文篡改、错误包装密钥、重启共享随机数存储以及并发
重复随机数竞争。所有失败路径均检查返回包为空且没有明文泄漏。
