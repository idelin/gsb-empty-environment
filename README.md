# 扩展主密钥恢复一致性

中间人借 TLS 1.2 会话恢复拼接两次握手（三次握手攻击）。为Go服务端写可导入的恢复准入包：会话记录保存是否使用 extended_master_secret；原会话用了而恢复请求没带则终止握手；原会话没用而恢复请求带了则不恢复，改走完整握手；协商了扩展主密钥的完整握手，主密钥须由会话哈希派生。PRF 由调用方注入，测全部组合及被篡改的会话哈希。

## 包：`example.org/netsec/q0276`（package `netsec`）

纯逻辑、不绑定任何 TLS 栈的 RFC 7627（extended_master_secret）恢复准入库，
用于防御 TLS 1.2 三次握手攻击（CVE-2014-1295）。

### 准入矩阵（RFC 7627 §5.3）

| 原会话 UsedEMS | ClientHello 携带 EMS | 决策 |
|---|---|---|
| 是 | 是 | 缩写握手恢复，继续使用 EMS |
| 是 | 否 | 终止握手，发送 fatal `handshake_failure`(40) |
| 否 | 是 | 拒绝恢复，回退完整握手并协商 EMS |
| 否 | 否 | 缩写握手恢复（传统模式） |

### 密钥派生（RFC 7627 §3）

- 完整握手且协商 EMS：`master_secret = PRF(pre_master_secret, "extended master secret", session_hash)`，
  种子仅为会话哈希（ClientHello..ServerHelloDone 的握手消息哈希），不含随机数。
- 完整握手未协商 EMS：传统 `PRF(pre_master_secret, "master secret", client_random || server_random)`。
- 缩写握手：直接复用缓存的主密钥，不重新派生。

### 典型用法

```go
import "example.org/netsec/q0276"

// 1. 解析 ClientHello 扩展块，得到 EMS 标志
offersEMS, err := netsec.ClientOffersEMS(clientHelloExtensionBlock)

// 2. 依据会话票据/缓存做准入
decision, err := netsec.Admit(cachedRecord, offersEMS)
switch decision.Action {
case netsec.ActionAbort:
    conn.sendAlert(netsec.AlertHandshakeFailure) // fatal
    return
case netsec.ActionFull:
    // 完整握手；decision.UseEMS 决定是否在 ServerHello 回显扩展
    masterSecret, err := netsec.DeriveMasterSecret(
        prf, decision.UseEMS, preMasterSecret,
        clientRandom, serverRandom, sessionHash)
    record := netsec.SessionRecord{
        Version:      netsec.VersionTLS12,
        UsedEMS:      decision.UseEMS,
        MasterSecret: masterSecret,
    }
    blob, _ := record.Marshal() // 存入票据/缓存
case netsec.ActionResume:
    // 缩写握手，直接使用 cachedRecord.MasterSecret
}

// PRF 由调用方按协商的密码套件注入，例如 SHA-256 套件：
prf := netsec.TLS12PRF(sha256.New)
```

### 测试

`go test ./...` 覆盖：四种恢复组合 + 无会话/异常记录、记录序列化往返与坏输入、
EMS/传统两条派生路径的 label 与 seed 断言、PRF 错误注入、独立实现交叉校验
`TLS12PRF`、扩展块解析、被篡改会话哈希经主密钥传播到 Finished `verify_data`
 的检测，以及完整握手→恢复/中止/回退的端到端生命周期。
