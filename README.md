# 5G NAS安全模式命令的能力回显核对

攻击者在注册阶段篡改终端上报的安全能力，诱使网络选出空加密或空完整性。为Go终端协议栈写可导入的 NAS 安全模式命令校验包：SMC 必须带完整性保护并通过用 KAMF 派生密钥算出的 MAC 验证；回显的 UE 安全能力须与注册请求里终端实际发出的字节完全一致（含 EPS 能力），否则回复 SMC 拒绝；选定的完整性算法为 5G-IA0 仅在紧急注册时允许。用能力被删位、IA0 和正常 SMC 验证。

## 用法

包路径为 `example.org/netsec/q0725`（包名 `netsec`），仅依赖 Go 标准库，内置
NIA2（AES-CMAC）与 HMAC-SHA-256 通用 KDF；NIA1/NIA3 可通过
`RegisterIntegrityAlgorithm` 注册扩展。

```go
// 1. 注册请求发出后，保存实际发送的 UE 安全能力原始字节（含 EPS 能力）
sentCaps, _ := netsec.ExtractUESecurityCapabilities(registrationRequest)

// 2. 校验下行 Security Mode Command
result, err := netsec.VerifySecurityModeCommand(smc, netsec.SMCConfig{
    KAMF:                   kamf,            // 主认证建立的 256 位 K_AMF
    UESecurityCapabilities: sentCaps,        // 注册请求中的能力字节
    ExpectedSequenceNumber: nasSeq,          // 期望的 NAS COUNT 序列号
    EmergencyRegistration:  isEmergency,     // 仅紧急注册允许 5G-IA0
})

// 3. 校验失败时返回 *ErrSecurityModeReject，Reject 字段为待发送的
//    SECURITY MODE REJECT 明文（cause #20/#23/#24）
if rj, ok := netsec.AsRejectError(err); ok {
    send(rj.Reject)
    return
}
// 成功时 result 给出选定的 NEA/NIA、序列号以及 K_nasint/K_nasenc
```

拒绝原因：NAS-MAC 或序列号不符 → cause #20（MAC failure）；回显能力与注册请求
任一比特不一致（含 EPS 能力被删位）→ cause #23；非紧急注册选择 5G-IA0、SMC
未完整性保护或算法不支持 → cause #24。

## 测试

`go test ./...` 覆盖：正常 NIA2 SMC、能力（含 EPS）被删位、5G-IA0 紧急/非紧急、
MAC 篡改、错误 K_AMF、序列号重放、明文 SMC、能力 IE 缺失、注册请求能力提取、
RFC 4493 AES-CMAC 向量与 KDF 确定性。
