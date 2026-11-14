# 邮件列表订阅确认组件

本包提供可导入的 Go double opt-in 状态机，供邮件列表服务在订阅、投递、退订和投诉处理路径中调用。它解决的核心问题是：**只有完成确认的地址，才能接收对应列表的邮件**。

## 安全保证

- 确认令牌使用 `crypto/rand` 生成 256 位随机值，服务端只保存令牌哈希。
- 每个令牌绑定规范化后的 `List`、`Address`、`Requester` 和 `ExpiresAt`。
- 令牌只能成功消费一次；验证与消费在同一把锁内完成，并发确认也只有一个能成功。
- 未确认、跨列表、地址不匹配、请求者不匹配、过期、已撤销和已消费的令牌全部拒绝。
- 失败的伪造、跨列表或地址篡改尝试不会消耗合法令牌。
- `Unsubscribe` 删除该列表的成员关系，并撤销该列表下尚未使用的确认令牌。
- `Complaint` 执行硬抑制：撤销该地址在所有列表的成员关系和待确认令牌，并阻止后续投递及新的确认请求。

## 用法

```go
manager, err := netsec.NewManager(netsec.Options{
    TokenTTL: 24 * time.Hour,
})
if err != nil {
    return err
}

// 1. 订阅请求。Requester 必须来自服务端可信身份，例如登录会话或账户 ID。
confirmation, err := manager.RequestSubscription(ctx, netsec.SubscriptionRequest{
    List:      "announce",
    Address:   "alice@example.com",
    Requester: session.ID,
})
if err != nil {
    return err
}

// 2. 只把 confirmation.Token 发送到 confirmation.Address。
//    不要把它返回给原始请求的浏览器，也不要抄送给其他地址。
sendConfirmationMail(confirmation.Address, confirmation.Token)

// 3. 用户点击确认链接后，使用同一 List、Address 和 Requester 消费令牌。
_, err = manager.ConfirmSubscription(ctx, netsec.ConfirmationRequest{
    Token:     tokenFromLink,
    List:      "announce",
    Address:   addressFromLink,
    Requester: session.ID,
})
if err != nil {
    return err
}

// 4. 每次真正发送列表邮件前必须经过投递闸门。
if err := manager.AuthorizeDelivery(ctx, "announce", "alice@example.com"); err != nil {
    return err // ErrNotConfirmed、ErrUnsubscribed、ErrComplained 等
}
sendListMail("alice@example.com")
```

退订与投诉：

```go
// 退订只影响指定列表；之后可重新走完整的 double opt-in。
err := manager.Unsubscribe(ctx, "announce", "alice@example.com")

// 投诉是跨列表的硬抑制，同时等同于退订。
err = manager.Complaint(ctx, "announce", "alice@example.com")
```

## 调用方约束

- **令牌只发给地址本人**：`RequestSubscription` 返回的令牌只能通过事务邮件发送到 `Confirmation.Address`。若原请求方能从 HTTP 响应读到令牌，攻击者就可用他人地址完成订阅。
- **请求者身份不可由表单控制**：`Requester` 必须是服务端生成/校验的稳定身份。若使用用户可填字段或固定字符串，转发确认的防护会失效。
- **投递前必须调用闸门**：实际发送邮件前调用 `AuthorizeDelivery`，非 `nil` 一律不发送。仅查询一次或在订阅时缓存结果不足以覆盖投诉和退订。
- **退订/投诉入口需鉴权**：本组件管理状态；公开的退订、投诉接口应由服务端校验签名链接、登录态或可信反馈渠道，避免被用于恶意抑制他人地址。
- **状态当前在进程内**：多实例部署必须把令牌、成员、退订和投诉状态放入共享事务存储，否则不同实例可能给出不一致的投递结论。请求入口也应做限流，避免确认令牌表被灌满。

## 语义

| 操作 | 成员关系 | 待确认令牌 | 投递结果 |
| --- | --- | --- | --- |
| 未确认 | 无 | 保留 | `ErrNotConfirmed` |
| 合法确认 | 创建 | 原子消费 | 允许 |
| 跨列表/地址/请求者不匹配 | 不变 | 保留 | 仍拒绝 |
| 过期/伪造/已消费 | 不变 | 拒绝 | 仍拒绝 |
| 退订 | 删除该列表成员 | 撤销该列表待确认令牌 | `ErrUnsubscribed` |
| 投诉 | 删除所有列表成员 | 撤销该地址所有待确认令牌 | `ErrComplained` |

## 验证

```sh
go test -race ./...
```

测试覆盖确认伪造、跨列表确认、地址篡改、转发确认、合法订阅、令牌过期、并发一次性消费、退订联动、投诉硬抑制，以及“只有同意地址可投递”的端到端断言。
