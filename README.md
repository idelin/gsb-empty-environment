# 票据年龄偏差

`netsec` 是可被 TLS 栈导入的 Go 包，用于校验 TLS 1.3
`obfuscated_ticket_age`。解码后的年龄不得超过“票据寿命 + 调用方偏差”；
超出窗口的票据不能用于恢复，窗口边界及以内的年龄可以通过。

```go
age := netsec.TicketAge{
    Obfuscated: identity.ObfuscatedTicketAge,
    AgeAdd:     ticketMeta.TicketAgeAdd,
    IssuedAt:   ticketMeta.IssuedAt, // 可选，提供时同时核对服务端观测年龄
    Lifetime:   ticketMeta.Lifetime,
}

if err := age.Validate(time.Now(), 10*time.Second); err != nil {
    // 丢弃该 PSK identity，继续尝试其他 identity 或执行完整握手。
}
```

只保留票据寿命时，可直接调用：

```go
err := netsec.ValidateTicketAge(obfuscatedTicketAge, ticketAgeAdd, lifetime, tolerance)
```

`DecodeTicketAge` 按 RFC 8446 以 `uint32` 环绕方式减去
`ticket_age_add`，结果单位为毫秒。`TicketAgePolicy` 也可单独用于已经解出的
年龄。
