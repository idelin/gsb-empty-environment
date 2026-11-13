# 5G NAS安全模式命令的能力回显核对

攻击者在注册阶段篡改终端上报的安全能力，诱使网络选出空加密或空完整性。为Go终端协议栈写可导入的 NAS 安全模式命令校验包：SMC 必须带完整性保护并通过用 KAMF 派生密钥算出的 MAC 验证；回显的 UE 安全能力须与注册请求里终端实际发出的字节完全一致（含 EPS 能力），否则回复 SMC 拒绝；选定的完整性算法为 5G-IA0 仅在紧急注册时允许。用能力被删位、IA0 和正常 SMC 验证。

## 包接口

导入路径：`example.org/netsec/q0725`

核心入口：

- `ValidateSecurityModeCommand(smc, sentRegistrationRequest, options)`：解析并校验原始 NAS 字节，返回 `Decision`。
- `ValidateParsedSecurityModeCommand(command, request, options)`：校验已解码的消息结构。
- `Decision.RejectMessage`：校验失败时已编码好的 `SECURITY MODE REJECT`；成功时为 `nil`。
- `DeriveKNASint(kamf, ia)` 与 `ComputeNASMAC(...)`：可用于终端侧独立复核密钥和 MAC。

```go
decision, err := netsec.ValidateSecurityModeCommand(
    receivedSMC,
    ueLocallyStoredRegistrationRequest, // UE 自己编码并发出的原始字节
    netsec.ValidationOptions{
        KAMF:   kamf,
        Count:  0, // 新安全上下文中的第一条下行 SMC
        Bearer: 0, // 3GPP 接入
    },
)
if err != nil {
    return err
}
if !decision.Accepted {
    sendNAS(decision.RejectMessage)
}
```

第二参数必须是 UE 本地保存的、实际编码发出的注册请求字节，不能使用网络侧收到后可能已被篡改的副本。校验器会逐字节比较 `0x2e` UE 安全能力 IE 的值，因此 5G EA/IA 和 EPS EEA/EIA 任一比特被改动都会返回 5GMM cause `#23`。

## 校验规则

- SMC 外层安全头必须是 `0x3`（新 5G NAS 安全上下文下的完整性保护），并带四字节 NAS MAC。
- `KNASint` 由 `KAMF` 经 5G KDF（`FC=0x69`、NAS integrity 类型 `0x01`、算法标识）派生，取 256 位输出的低 128 位。
- NIA2 使用 AES-128-CMAC，MAC 覆盖序列号及其后的完整明文 NAS 消息。
- 回显能力必须与 UE 实际发送值完全一致，包含 EPS 加密/完整性能力字节。
- 选定的 5G-EA/5G-IA 必须出现在 UE 实际能力位图中。
- 5G-IA0 仅在紧急注册时允许；普通注册直接返回 cause `#24`。
- MAC 失败、缺少新上下文完整性保护或密文中断时均失败关闭并返回 `SECURITY MODE REJECT`。

内置完整算法实现为 5G-IA0 和 5G-IA2。需要 5G-IA1/5G-IA3 时，可通过 `ValidationOptions.MACFunc` 注入协议栈已有的实现；未注入时未知算法不会放行。

## 测试

```sh
go test ./...
```

测试覆盖正常 IA2 SMC、能力位被删（含 5G 和 EPS 字节）、普通注册 IA0、紧急注册 IA0、错误 MAC、缺少新上下文完整性保护、未声明的空加密选择，以及 RFC 4493 AES-CMAC 向量。
