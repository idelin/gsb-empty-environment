# RTP源绑定与SSRC劫持防护

语音网关接收 RTP 的 UDP 端口对外开放，攻击者伪造同一 SSRC，或发送序列号突然大幅跳变的包，让接收端重新同步到伪造流，也可以用伪造的 RTCP BYE 中断通话。为Go媒体网关写可嵌入接收路径的源绑定包，序列号校验采用 RFC 3550 附录 A.1 的 update_seq，MAX_DROPOUT 取 3000，MAX_MISORDER 取 100，MIN_SEQUENTIAL 取 2：每个会话首个通过校验的包锁定其源地址与端口，之后同一 SSRC 来自其他地址的包丢弃；序列号落在 MAX_DROPOUT 之外时，不得立即重同步，丢弃并计数，只有连续两个相邻序号才重同步；同一地址发来新的 SSRC 而旧 SSRC 仍活跃时，不得替换，除非调用方的信令声明了 SSRC 变更；RTCP BYE 仅在发送地址等于已锁定地址时才生效；SSRC 与本端相同视为碰撞，换一个新 SSRC 并重新宣告。用伪造源地址、序号大跳变、连续两包重同步、伪造 BYE 和 SSRC 碰撞验证。

## 实现

`package netsec` 提供无外部依赖、可直接嵌入接收循环的绑定器，核心类型为 `Session`。

- `sourcebind.go`：源绑定状态机与 RFC 3550 附录 A.1 的 `update_seq` 实现。
- `rtcp.go`：RTP/RTCP 解析与 RTCP BYE 的锁定地址校验。
- `sourcebind_test.go`：攻击场景与边界条件测试。

常量按需求固定：`MAX_DROPOUT=3000`、`MAX_MISORDER=100`、`MIN_SEQUENTIAL=2`。

## 安全规则

- **源地址锁定**：每个 SSRC 首个通过 `update_seq` 校验的包锁定其 UDP 源地址（IP+端口）；
  之后同一 SSRC 来自其他地址的包按 `DropSpoofedSource` 丢弃，且不污染序列号状态。
- **大跳变不立即重同步**：`udelta` 超出 `MAX_DROPOUT` 且不落在回绕区间时，包被丢弃并计入
  `LargeJumpDrops`，仅记录 `badSeq=seq+1`；只有紧接着收到与 `badSeq` 相等的相邻序号，
  才按 RFC 大跳分支重同步（结果中 `Resynced=true`）。
- **观察期**：新源进入 `MIN_SEQUENTIAL` 观察期，首包 + 一个相邻序号（共两个包）才锁定；
  观察期内出现序号空洞则以最新序号重新计时，结果中 `Probation=true`。
- **SSRC 变更需信令授权**：同一地址在旧 SSRC 仍活跃时送来新 SSRC，默认按
  `DropUnacceptedSSRC` 拒绝；只有先调用 `DeclareSSRCChange(old, new)` 声明后才允许替换。
- **BYE 地址校验**：RTCP BYE 只有发送地址等于该 SSRC 的锁定地址才会终止源，
  否则整个包按伪造丢弃（`BYESpoofed` 计数），通话不中断。
- **SSRC 碰撞**：收到与本端相同 SSRC 的包即视为碰撞，本端通过随机源另选不冲突的新 SSRC，
  碰撞包丢弃，并通过 `OnCollisionReannounce` 回调通知上层重新宣告（SDES/RTCP）。

## 嵌入接收路径

```go
sess := netsec.NewSession(localSSRC, nil)
sess.OnCollisionReannounce = func(newSSRC uint32) {
    // 用 newSSRC 重新发送 SDES/RTCP 宣告
}

// RTP 数据报
switch r := sess.HandleRTP(buf, raddr); {
case r.Collision:
    // 本端已换新 SSRC，回调会触发重新宣告；碰撞包丢弃
case r.Accepted:
    // 交给去抖动/解码
case r.Probation:
    // 观察期，统计但不解码
default:
    // r.Drop 说明丢弃原因，接入监控
}

// RTCP 数据报
if rr := sess.HandleRTCP(buf, raddr); rr.OK && rr.BYE {
    // 仅当 BYE 来自锁定地址时 rr.TerminatedSSRCs 才非空
}

// 信令层得知对端切换 SSRC 时
sess.DeclareSSRCChange(oldSSRC, newSSRC)
```

`SnapshotStats()` 返回各类丢弃/重同步/碰撞计数器，`IsActive`/`SourceCount` 用于查询活跃源。

## 验证

```sh
go test -race -count=1 ./...
```

测试覆盖：伪造源地址（含同 IP 不同端口）、序号大跳变不立即重同步、连续两个相邻序号重同步、
伪造与合法 RTCP BYE、未授权/授权 SSRC 变更、SSRC 碰撞重新宣告、序列号回绕、
`MAX_DROPOUT` 边界、观察期空洞重启、畸形包以及并发攻击流量。
