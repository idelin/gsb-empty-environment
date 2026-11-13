# RTP 源绑定与 SSRC 劫持防护

`SourceBinder` 是面向单个 RTP 接收会话的防护组件。它锁定首个被接受的
RTP 包的 UDP 源地址和端口，随后在转发前完成 SSRC、序列号及 RTCP BYE
校验。

## 防护行为

- 首个被接受的 RTP 包将 `(IP, UDP port)` 锁定；之后来自其他端点的包全部丢弃。
- 序列号使用 RFC 3550 附录 A.1 的 `update_seq`，`MAX_DROPOUT=3000`、
  `MAX_MISORDER=100`、`MIN_SEQUENTIAL=2`。
- 序列号大跳变首包丢弃并计入 `SequenceJumpDrops`，只有紧邻的下一个序号
  才能触发重同步；重同步包本身被接受。
- 旧 SSRC 活跃时，同端点发来新 SSRC 会被丢弃。调用方必须先通过可信信令
  执行 `DeclareSSRCChange(newSSRC)`，再放行新 SSRC 的首包。
- RTCP BYE 必须同时满足：来源端点等于已锁定端点、SSRC 等于当前活动流。
  伪造 BYE 计入 `ForgedBYEDrops`。
- 收到与本端 `LocalSSRC` 相同的 SSRC 时视为碰撞。组件生成新 SSRC，并通过
  `WithSSRCChangeHandler` 回调通知信令层重新宣告。

## 接入示例

```go
binder := netsec.NewSourceBinder(localSSRC,
    netsec.WithSSRCChangeHandler(func(oldSSRC, newSSRC uint32) {
        // 信令层在此为 oldSSRC 发送 RTCP BYE/SDES，并宣告 newSSRC。
    }),
)

for {
    packet, source, _ := conn.ReadFromUDPAddrPort(buf)
    if isRTP(packet) {
        decision, err := binder.HandleRTPPacket(packet, source)
        if err != nil || !decision.Accepted {
            continue
        }
        forward(packet)
    } else {
        decisions, err := binder.HandleRTCPPacket(packet, source)
        if err != nil {
            continue
        }
        for _, decision := range decisions {
            if decision.Accepted {
                terminateCall()
                break
            }
        }
    }
}
```

解析后的字段也可以直接送入 `HandleRTP(ssrc, seq, source)` 和
`HandleRTCPBYE(ssrc, source)`。所有方法均可在并发收包路径中调用；回调在锁外
执行。`Stats` 返回累计丢弃、重同步和碰撞计数。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```
