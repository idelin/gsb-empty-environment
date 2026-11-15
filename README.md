# STP根桥抢占防护

攻击者在边缘端口发最高优先级BPDU，想成为根桥并劫持流量。给Go交换机协议栈写供BPDU接收路径调用的守卫：按根桥ID、路径开销、发送桥ID、端口ID比较优先级向量；边缘或根防护端口收到更优BPDU时丢弃并让端口停止转发，直到静默期结束；每端口TCN速率有限，消息年龄不小于最大年龄的BPDU丢弃。用伪造根桥、TCN洪泛和合法拓扑变更验证。

## 用法

解析后的BPDU交给`Guard.Receive`（或`ProcessBPDU`）。守卫返回准入结果和端口应处的转发状态；交换机接收路径只应在`Accepted`为`true`时继续处理该BPDU，并按`State`/`Forwarding`更新端口。

```go
guard := netsec.NewGuard(netsec.GuardConfig{
	LocalBridgeID: netsec.BridgeID{
		Priority: 0x8000,
		MAC:      [6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
	},
	QuietPeriod: 30 * time.Second,
	MaxAge:      20 * time.Second,
	TCNRate:     1, // 每端口每秒1个TCN
	TCNBurst:    2,
})

guard.AddPort(netsec.PortConfig{ID: 1, Edge: true})
guard.AddPort(netsec.PortConfig{ID: 2, RootGuard: true})

result := guard.Receive(1, bpdu)
if !result.Accepted {
	// 丢弃BPDU；若result.State为StateBlocking，在BlockedUntil前停止转发。
}
```

行为要点：

- 优先级向量按根桥ID、根路径开销、发送桥ID、端口ID依次做无符号字典序比较，较小者更优。
- `RootVector`可注入当前已知根路径；否则以本地桥为初始基线。只有通过准入的非防护端口BPDU才能更新基线。
- 边缘端口或根防护端口收到优于基线的配置BPDU时，立即丢弃并将端口置为阻塞；新的更优BPDU会延长静默期，静默期结束后恢复转发。
- 每个端口使用独立令牌桶限制TCN（`Type == TCNBPDU`或配置BPDU的拓扑变更标志），超限只丢弃TCN，不改变转发状态。
- `MessageAge >= MaxAge`的BPDU被丢弃；BPDU携带更小的`MaxAge`时采用更严格的值。
- `Now`字段可注入假时钟，便于确定性测试静默期和令牌桶。

运行验证：

```sh
go test -race ./...
go vet ./...
```
