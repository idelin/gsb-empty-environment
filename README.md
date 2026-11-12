# QUIC早期数据重放窗口

攻击者录下客户端的QUIC 0-RTT数据包，转发给集群里的另一个节点重放。给Go QUIC服务端写供握手路径调用的早期数据准入包：会话票据带签发时间和age_add，按RFC 8446第8.3节比较混淆后的票龄与服务端实测票龄；超出调用方给的窗口、票龄32位回绕错位或PSK binder已在重放记录中，都拒绝0-RTT并退回1-RTT，不得断开连接。记录库不可用时同样退回，记录写入成功后才放行。用跨节点重发、窗口边界和合法单次0-RTT验证放行次数。

## earlydata 包

`earlydata` 是握手路径调用的 0-RTT 准入包，仅依赖标准库：

- `Gate.Admit` 按 RFC 8446 §8.3.10 用 `(实测票龄 + ticket_age_add) mod 2^32`
  重算混淆票龄，与 ClientHello 的 `obfuscated_ticket_age` 做 32 位模差比较；
  超窗返回 `ReasonAgeSkew`，实测票龄达到 2^32 毫秒（必然回绕错位）返回
  `ReasonAgeOverflow`。
- 票龄校验通过后调用 `ReplayStore.CheckAndMark`（原子“不存在则创建”）：
  binder 已存在返回 `ReasonReplay`，存储报错返回 `ReasonStoreUnavailable`，
  只有首次写入成功才 `Allowed=true`。
- 所有拒绝均为普通返回值而非错误/告警：握手路径忽略早期数据并继续 1-RTT
  握手，连接不断开。
- `MemoryStore` 为带 TTL 的内存参考实现；跨节点防重放需提供集群共享的
  `ReplayStore`（如 Redis `SET NX PX`、分布式 KV 条件写、数据库唯一键）。

集成骨架：

```go
gate, err := earlydata.NewGate(earlydata.Config{
    Window:    10 * time.Second, // 调用方给定的票龄偏差窗口
    RecordTTL: ticketLifetime,    // 覆盖票据剩余可接受生命周期
    Store:     clusterReplayStore,
})

res := gate.Admit(ctx, earlydata.Request{
    TicketIssuedAt: ticket.IssuedAt,   // 解密票据得到的签发时间
    TicketAgeAdd:   ticket.AgeAdd,
    ObfuscatedAge:  ch.ObfuscatedTicketAge,
    Binder:         ch.SelectedPSKBinder,
})
if !res.Allowed {
    // 忽略/缓冲 0-RTT 数据，按 1-RTT 继续握手，不得关闭连接
}
```

验证：`go test -race ./earlydata/`，覆盖跨节点重发、窗口边界（±窗口与超 1ms）、
32 位回绕与溢出、记录库故障失败关闭、TTL 过期、并发下仅放行一次。
