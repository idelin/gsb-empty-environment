# STP根桥抢占防护

攻击者在边缘端口发最高优先级BPDU，想成为根桥并劫持流量。给Go交换机协议栈写供BPDU接收路径调用的守卫：按根桥ID、路径开销、发送桥ID、端口ID比较优先级向量；边缘或根防护端口收到更优BPDU时丢弃并让端口停止转发，直到静默期结束；每端口TCN速率有限，消息年龄不小于最大年龄的BPDU丢弃。用伪造根桥、TCN洪泛和合法拓扑变更验证。

## 行为

- 优先级向量按 `RootID`、`RootPathCost`、`SenderBridgeID`、`SenderPortID` 的字典序比较，数值越低越优。
- `Edge` 端口把收到的配置BPDU与本桥优先级向量比较；`RootGuard` 端口与当前最优根向量比较。更优BPDU被丢弃，端口进入非转发状态，静默期内再次收到更优BPDU会重新计时。
- 静默期结束后端口自动恢复转发；期间收到的合法BPDU仍可交给STP处理，但不会提前恢复转发。
- TCN按端口、固定时间窗限速。超限TCN被丢弃，不影响端口转发状态。
- `MessageAge >= MaxAge` 的BPDU在防护和限速之前丢弃。BPDU未携带有效 `MaxAge` 时使用 `GuardConfig.MaxAge`。
- 守卫不直接操作数据面：调用方必须根据 `BPDUResult.Accepted` 丢弃报文，并根据 `BPDUResult.Forwarding` / `QuietUntil` 更新端口状态。

## 用法

```go
guard := netsec.NewGuard(netsec.GuardConfig{
	LocalBridgeID:   netsec.BridgeID{Priority: 0x8000, MAC: localMAC},
	QuietPeriod:     20 * time.Second,
	TCNRateLimit:    2,
	TCNRateInterval: time.Second,
})
guard.ConfigurePort(portID, netsec.PortConfig{Edge: true})

result, err := guard.HandleBPDU(portID, received)
if err != nil {
	// 未配置端口：按故障关闭策略处理，不要继续解析。
	return
}
if !result.Accepted {
	// 丢弃报文。
}
if !result.Forwarding {
	// 端口停止转发，直到 result.QuietUntil。
}
```

时钟可通过 `GuardConfig.Clock` 注入，便于确定性地验证静默期与限速窗口。

## 验证

`go test -race ./...` 覆盖伪造根桥、根防护、静默期续期与恢复、按端口TCN洪泛限速、消息年龄边界，以及合法配置变更和TCN。
