# ikecookie — IKE_SA_INIT 准入控制

针对伪造源地址的 IKE_SA_INIT 洪泛（耗尽半开连接表与 DH 运算），按
RFC 7296 §2.6 实现的可导入 Go 准入包。仅依赖标准库。

导入路径：`example.org/netsec/q0752/ikecookie`

## 策略

- 半开连接数低于阈值（默认 `200`，`WithHalfOpenLimit` 可调）时，所有
  IKE_SA_INIT 正常处理。
- 达到阈值后，不带 COOKIE 通知的请求只回 COOKIE，**不做 DH、不占半开表项**。
- 重发请求带回 COOKIE 时不再下发新挑战：校验通过才处理（并占一个半开表项），
  校验失败按 RFC 静默丢弃（`DecisionDrop`）。
- COOKIE 值为 `HMAC-SHA256(秘密, Ni || 源地址 || SPIi)`，字段做长度分帧以避免
  拼接歧义；IPv4/IPv6 分别绑定 4/16 字节地址并带版本字节。
- 校验要求 Ni、源地址、SPIi 与签发时完全一致，任一被篡改即失效；比较使用
  `hmac.Equal` 恒定时间比较。
- 秘密定时轮换（默认 5 分钟，`RotateSecret` 可手动轮换）；轮换后上一把秘密
  保留一个周期，期间旧 COOKIE 仍有效，再轮换一次才彻底失效。
- 半开表项带 TTL（默认 30s），由后台 janitor 回收，防止异常对端永久占槽。

## 判定结果

| Decision | 含义 | 网关动作 |
| --- | --- | --- |
| `DecisionAllow` | 放行，已预留半开槽（返回 `Release`） | 执行 DH；SA 完成或放弃时调 `Release` |
| `DecisionCookie` | 需质询 | 仅回 COOKIE 通知，禁止 DH |
| `DecisionDrop` | COOKIE 校验失败 | 静默丢弃 |
| `DecisionInvalid` | 请求本身非法（如源地址无效） | 丢弃 |

## 用法

```go
gate, _ := ikecookie.NewAdmitter() // 可用 Option 调整阈值/TTL/轮换周期
defer gate.Close()

res := gate.Admit(ikecookie.InitRequest{
    SourceIP: srcAddr,          // netip.Addr
    SPIi:     spii,             // [8]byte
    Ni:       niPayload,        // 发起方 nonce
    Cookie:   cookieNotify,     // 无 COOKIE 时为 nil
})
switch res.Decision {
case ikecookie.DecisionAllow:
    defer res.Release()
    // ... 执行 DH，继续 IKE 协商
case ikecookie.DecisionCookie:
    // 把 res.Cookie 放进 COOKIE 通知回复，不做 DH
case ikecookie.DecisionDrop, ikecookie.DecisionInvalid:
    // 丢弃
}
```

完整可运行示例见 `example_test.go` 的 `ExampleAdmitter`。

## 验证场景

`go test -race -count=1 ./...` 覆盖：

- 无 COOKIE：阈值下放行并占槽，阈值上只回 COOKIE 且不占槽、不做 DH；
- 换 Ni：带旧 COOKIE 但 Ni 改变 → 丢弃；
- 换源地址：带旧 COOKIE 但源 IP 改变（含 IPv6）→ 丢弃；
- 换 SPIi：SPIi 改变 → 丢弃；
- 正常流程：质询 → 带匹配 COOKIE 重发 → 放行，且 Ni 与签发时一致；
- 秘密轮换：一次轮换后旧 COOKIE 仍可用，两次轮换后失效；自动轮换同理；
- 半开 TTL 回收、并发安全（race 检测）、HMAC 分帧 golden 向量。
