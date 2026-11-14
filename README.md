# Kerberos FAST Session State

供 KDC 前端调用的 FAST 预认证会话状态组件。它不实现 ASN.1/Kerberos 编解码器，只接收严格 DER 解析后的 `ASReq`/`TGSReq`（或通用的 `Request`）、原始加密字段，以及调用方提供的 finished checksum 校验器。

## 安全边界

- 会话身份绑定：客户端 principal 的 `NameType` 与规范组件、外层 realm、目标 realm、KDC 实例、密钥代次、armor ticket 会话（ID/realm/client）、nonce、enctype、请求体摘要和随机 cookie 句柄。
- Cookie 使用 AES-256-GCM；明文载荷包含上述绑定、随机句柄、状态阶段和绝对过期时间。AAD 绑定协议版本、前端分区和密钥代次。
- 下一请求必须匹配外层 realm、armor ticket、finished checksum、请求体摘要及全部会话绑定。
- `Consume` 在单个互斥区内原子删除状态；并发消费同一 cookie 只有一个调用成功。`Advance` 原子地把旧句柄替换为新句柄。
- 所有 cookie 无效、跨 realm、armor 替换、重放、摘要不符和过期错误均返回同一个 `ErrSessionRejected`，不泄露失败原因。
- `EncryptedField` 和 `UnknownDER` 不进入 cookie，也不会出现在错误中。
- 跨 realm referral 使用 `Refer`：旧状态被消费，目标状态使用全新的句柄、过期时间、阶段和调用方明确给出的下一请求绑定，不复制旧 cookie 内容。

## 基本用法

```go
keyring, err := netsec.NewKeyring("kdc-a", 1, cookieKey) // 32-byte AES key
if err != nil {
    return err
}

manager, err := netsec.NewManager(netsec.Config{
    FrontendPartition: "frontend-a",
    KDCInstance:       "kdc-a",
    ProtocolVersion:   1,
    Keyring:           keyring,
    FinishedVerifier:  verifier, // 校验 finished checksum
    CookieTTL:         2 * time.Minute,
})
if err != nil {
    return err
}

req := netsec.Request{
    Kind:        netsec.RequestKindAS,
    Client:      principal,
    OuterRealm:  "REALM.A",
    TargetRealm: "REALM.A",
    Nonce:       nonce,
    Enctype:     enctype,
    BodyDigest:  netsec.DigestRequestBody(kdcReqBodyDER),
    Armor:       armor,
    Finished:    finished,
}

cookie, err := manager.Begin(ctx, req)
phase, err := manager.Validate(ctx, cookie, req) // 只检查，不消费
nextCookie, err := manager.Advance(ctx, cookie, req, netsec.PhasePreAuthVerified)
err = manager.Consume(ctx, nextCookie, req)
```

`ASReq`/`TGSReq` 可直接交给 `BeginAS`、`BeginTGS` 或 `BeginParsed`，也可通过 `ToRequest` 转换。

## 多轮预认证

`PhasePreAuthPending -> PhasePreAuthVerified -> PhaseReady` 只能向前迁移，也允许停留在当前阶段处理下一轮 challenge。请求体摘要和 nonce 应在多轮中保持不变；padata、加密字段和 finished checksum 可以变化。`Advance` 返回新句柄，旧 cookie 立即失效，绝对过期时间不会因轮次而延长。

## Referral

```go
next := netsec.Request{
    Kind:        netsec.RequestKindTGS,
    Client:      req.Client,
    OuterRealm:  "REALM.B",
    TargetRealm: "REALM.B",
    // nonce、enctype、body digest、armor 等目标路径绑定
}
referralCookie, err := manager.Refer(ctx, sourceCookie, currentRequest, next)
```

`Refer` 仅允许 TGS，且必须保持同一客户端并改变目标 realm。目标状态的 `Finished` 字段在 referral 时被忽略，等下一请求到来时再由 `Validate`/`Consume` 校验。

## 密钥轮换

`Keyring.Rotate`、`Manager.RotateKey` 会激活更高的代次；只有当前代次可打开 cookie，因此轮换会使旧 cookie 失效。已安装代次只能通过 `InstallKey`/`Activate` 向前切换；任何后退或重复激活返回 `ErrKeyGenerationRollback`。共享 `Keyring` 的多个 Manager 应由部署层协调轮换。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖跨 realm/partition/instance cookie 搬运、armor 替换、nonce 与旧句柄重放、并发消费、密钥轮换与回滚、referral 新状态、过期不可区分性、未知字段不回显和多轮预认证。
