# 扩展主秘密强制

TLS1.2恢复会话时没有extended_master_secret，仍算出主秘密。双方都支持时必须使用扩展主秘密；只有一方有或恢复时丢失该标志则失败。双方都有且会话标志一致时放行。可导入的Go库供TLS栈调用。从恢复票据里去掉该标志。

## 策略矩阵

完整握手（`NegotiateEMS`）：

| 客户端 | 服务端 | 结果 |
| --- | --- | --- |
| 有 EMS | 有 EMS | 协商 EMS，用会话哈希派生扩展主秘密 |
| 无 EMS | 无 EMS | 走经典主秘密（随机值作 seed） |
| 仅一方有 EMS | — | 返回 `ErrEMSNegotiationMismatch`，握手失败 |

会话恢复（`ValidateResumption`）：先应用上表，再要求协商结果与原始会话标志一致：

| 原会话 | 恢复协商 | 结果 |
| --- | --- | --- |
| EMS | 双方均有 EMS | 放行，复用原主秘密、派生新密钥块 |
| 经典 | 双方均无 EMS | 放行 |
| EMS | 无 EMS | `ErrEMSResumptionLost` |
| 经典 | 有 EMS | `ErrEMSResumptionUnexpected` |
| 任意 | 仅一方有 EMS | `ErrEMSNegotiationMismatch` |

任何策略失败都不会返回主秘密或密钥块。

## 票据设计

`TicketData` 只封装不透明的服务端会话 ID，再由 `TicketCipher` 做
AES-256-GCM 认证加密。票据中没有 EMS 标志、没有主秘密：EMS 状态只保存在
服务端 `SessionStore`（`Session.ExtendedMasterSecret`），客户端仅在本地
`MemoryClientSessionCache` 记录，恢复时以服务端存储为准，因此无法通过篡改或
剥离票据标志来降级。

## 主要 API

- `NegotiateEMS(client, server bool) (bool, error)`：完整握手扩展协商
- `ValidateResumption(client, server bool, *Session) error` /
  `ValidateResumptionFlag(...)`：恢复策略校验
- `DeriveMasterSecret(...)`：按 EMS/经典两种 seed 派生 48 字节主秘密
- `DeriveKeyBlock(...)`、`VerifyData(...)`、`PRF(...)`、`SessionHash(...)`
- `Server.FullHandshake` / `Server.Resume`：服务端编排
- `MemoryClientSessionCache.FullHandshake` / `.Resume`：客户端编排
- `TicketCipher.Seal` / `.Open`、`MemorySessionStore`：票据与会话存储

## 用法示例

```go
store := netsec.NewMemorySessionStore()
tickets, _ := netsec.NewTicketCipher(key32, 1)
server := &netsec.Server{Store: store, Tickets: tickets}

// 完整握手：双方都带 EMS 扩展时强制使用扩展主秘密
result, ticket, err := server.FullHandshake(&netsec.HandshakeInput{
    PreMasterSecret:     preMaster,
    ClientRandom:        clientRandom,
    ServerRandom:        serverRandom,
    Transcript:          transcript,
    ClientAdvertisesEMS: true,
    ServerAdvertisesEMS: true,
    Lifetime:            time.Hour,
})

// 恢复：标志与原会话不一致时直接返回错误，不返回任何秘密
resumed, err := server.Resume(&netsec.ResumeInput{
    Ticket:              ticket,
    ClientRandom:        newClientRandom,
    ServerRandom:        newServerRandom,
    Transcript:          resumeTranscript,
    ClientAdvertisesEMS: true,
    ServerAdvertisesEMS: true,
})
```

## 测试

```sh
go test ./... -race -cover
```

覆盖完整握手与恢复的全部 EMS 组合、票据篡改/过期/密钥轮换、存储防御性拷贝与
并发安全，以及 PRF/主秘密/密钥块的独立 HMAC 交叉校验。
