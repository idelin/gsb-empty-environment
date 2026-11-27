硬件安全密钥宿主与无密码认证中间件在实现 FIDO2 CTAP2（v2.1）`ClientPIN`（PIN/UV Auth Protocol Two）及凭证保护扩展（`credProtect`）时，若误用同一对称密钥同时做加密与 MAC、未强制校验随机 IV 与共享点有效性、在 PIN 连续失败达上限后未锁死重试计数器，或忽略 `credProtect` 级别 2/3 导致未完成用户验证（UV）即可通过 `getAssertion` 静默枚举可发现凭证（Resident Key），攻击者可暴力破解 PIN 或无感窃取凭据元数据。为 Go 认证宿主实现可导入的 CTAP2 PIN/UV Auth Protocol 2 与 `credProtect` 策略验证包，严禁联网（仅用标准库 `crypto/ecdh`/`crypto/aes`/`crypto/hmac`/`crypto/sha256`）。组件严格实现：（1）P-256 ECDH 密钥协商与 Protocol Two 独立双密钥派生（`hmacKey = HKDF-SHA-256(Z, salt=0x00*32, info="CTAP2 HMAC key", L=32)`、`aesKey = HKDF-SHA-256(Z, salt=0x00*32, info="CTAP2 AES key", L=32)`），强制使用 16 字节随机 IV 进行 AES-256-CBC 加解密并完整计算 HMAC-SHA-256（取全 32 字节），硬拒绝低阶/非法曲线点与 Protocol One 截断 16 字节 MAC 降级；（2）精确维护 `pinRetries`（上限 8 次）、单次上电连续失败上限（3 次触发 `CTAP2_ERR_PIN_AUTH_BLOCKED`）及归零永久锁定（`CTAP2_ERR_PIN_BLOCKED`），并校验 `pinUvAuthToken` 的权限位掩码（`mc`/`ga`/`cm`/`be`）与绑定 RP ID 不可跨站点复用；（3）完整执行 `credProtect` 1/2/3 级访问控制：级别 2 在无 `allowList` 时必须有效 UV，级别 3 无论有无 `allowList` 一律强制有效 UV，且不返回未授权凭证信息。附双密钥派生向量、Protocol One 降级拦截、PIN 锁定状态机与 `credProtect` 枚举阻断测试。

## 实现：`ctap2auth` 包

纯标准库实现（`crypto/ecdh`、`crypto/aes`+`crypto/cipher`、`crypto/hmac`、
`crypto/sha256`、`crypto/rand`），无第三方依赖、无网络访问。

| 文件 | 职责 |
| --- | --- |
| `ctap2auth/ecdh.go` | P-256 ECDH 密钥生成/协商，强制 SEC1 编码与曲线点有效性校验，拒绝低阶/非法点 |
| `ctap2auth/hkdf.go` | RFC 5869 HKDF-SHA-256（空盐回退为 32 字节零盐） |
| `ctap2auth/protocol2.go` | 独立双密钥派生、AES-256-CBC（16 字节随机 IV、拒绝零 IV）、全 32 字节 HMAC、Protocol One 降级拦截 |
| `ctap2auth/session.go` | ECDH 会话封装（点校验 → Z → 双密钥 → 加解密/MAC） |
| `ctap2auth/pin.go` | `pinRetries=8`、上电 3 连败 `PIN_AUTH_BLOCKED`、归零 `PIN_BLOCKED`、token 权限位与 RP 绑定 |
| `ctap2auth/credprotect.go` | credProtect 1/2/3 访问控制与可发现凭证枚举过滤 |
| `ctap2auth/errors.go` | CTAP2.1 状态码（0x14/0x27/0x31–0x37）与可 `errors.Is` 的哨兵错误 |

### 关键安全语义

- 双密钥隔离：`CTAP2 HMAC key` 与 `CTAP2 AES key` 经不同 HKDF `info` 独立派生，绝不复用同一把密钥。
- 点校验：`crypto/ecdh` 执行 NIST SP 800-56A 公钥校验，叠加 SEC1 前缀/长度/全零检查，杜绝无效曲线与小子群攻击。
- 随机 IV：每次 CBC 加密新生成 16 字节 IV 并前置；解密时硬拒绝全零 IV，严格校验 PKCS#7 填充。
- MAC 不截断：仅接受全 32 字节 HMAC-SHA-256；16 字节标签直接返回 `ErrProtocolOneDowngrade`。
- 锁死状态机：密码学失败计入上电三连败（不动 `pinRetries`），PIN 不匹配递减 `pinRetries`，归零永久锁定且不随上电恢复；`PowerCycle` 仅清除易失性三连败与 token。
- token 最小授权：`mc`/`ga` 必须绑定 RP ID 且跨站点返回 `ErrRPIDMismatch`；`cm`/`be` 为无 RP 作用域的管理权限。
- credProtect：无 `allowList` 且无 UV 时仅返回级别 1；级别 3 即使带 `allowList` 也强制 UV；未授权凭证被整体过滤，不泄露任何元数据。

### 离线测试

```sh
GOPROXY=off go test ./... -count=1
GOPROXY=off go test ./... -race -count=1
```

覆盖：RFC 5869 HKDF 向量、固定标量双密钥派生黄金向量、非法/低阶点拒绝、
Protocol One 16 字节 MAC 降级拦截、随机/零 IV 与坏填充、PIN 锁定状态机、
token 权限与 RP 跨站复用拦截、credProtect 三级枚举阻断及端到端 ClientPIN 流程。
