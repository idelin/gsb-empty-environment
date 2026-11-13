# 优雅重启保留了已撤权邻居路由

BGP邻居重启期间路由器会暂留旧路由，若安全策略或ROA已变化，攻击者可借stale路由继续引流。给Go路由宿主实现可导入的优雅重启账本：会话身份绑定neighborAS、routerID、transport代次和策略版本；断线时只有对端此前协商重启能力且未发生认证失败的会话可把路由标Stale，并记录每条routeID、前缀、属性摘要、原策略/VRP版本和stale截止。重连必须同一neighborAS/routerID且新transport代次，收到End-of-RIB前逐条重新公告可刷新routeID；新公告重新跑当前导入策略和当前VRP，成功才替换Stale。策略/VRP更新时立即重验全部Stale，失效者撤销，不等计时器；截止或End-of-RIB到达后未刷新的全部撤销。任何Stale路由只能在配置的restart时限内在配置的restart时限内继续转发，不能作为向其他邻居导出的新依据，也不能参与FlowSpec验证。实现虚拟时钟和路由选择回调，交错断线、策略更新、重连和EOR，搜索待审实现在撤权后仍转发或再导出Stale路由的最短轨迹。覆盖routerID复用、认证失败断线、重复EOR和计时边界。

## 实现

`package netsec` 提供可直接导入的优雅重启账本：

- `Ledger`：会话、fresh 路由、stale 路由、撤销历史和回调的唯一所有者；并发安全。
- `Session`：绑定 `(NeighborAS, RouterID, TransportGeneration, PolicyVersion)`。同一 peer 重连必须使用严格递增的 transport 代次。
- `VirtualClock`：手动推进时间；每次查询都会惰性执行截止撤销，`Ledger.Advance` 也会立即清理。
- `Selector`：只接收对当前用途合法的候选。`UseForward` 可看到仍在窗口内的 stale 路由；`UseExport` 和 `UseFlowSpec` 永远看不到 stale 路由。
- `SearchShortestViolation`：对断线、策略/VRP 更新、重连、公告、EOR 和时钟推进的所有交错顺序做 BFS 重放，返回最短的 stale 路由安全违例轨迹；正确实现返回 `(nil, nil)`。

核心约束：

1. 只有断线前协商了 graceful-restart 且没有认证失败的会话，才能把该代次的 fresh 路由转为 stale。
2. stale 记录保存 route ID、前缀、属性摘要、原 policy/VRP 版本、transport 代次和绝对截止时间。
3. 策略或 VRP 更新先处理截止边界，再对全部 stale 路由逐条重验；失效路由立即撤销，无需等待计时器。
4. 重连只在 `(NeighborAS, RouterID)` 相同且 transport 代次更新时建立。收到 EOR 前的重新公告会先跑当前策略和当前 VRP，成功后替换 stale 记录；失败不会刷新 stale。
5. EOR（包括重复 EOR）和截止时间都会撤销所有未刷新的旧代次 stale 路由；重复 EOR 不影响已刷新或新公告的 fresh 路由。
6. stale 路由从未进入导出候选或 FlowSpec 候选；即使自定义选择器返回 stale 视图用于导出/FlowSpec，账本也会拒绝该结果。

## 使用示例

```go
clock := netsec.NewVirtualClockAt(time.Unix(1000, 0))
ledger := netsec.NewLedger(netsec.Config{
    RestartTime: 30 * time.Second,
    Clock:       clock,
    ImportPolicy: func(netsec.Route) bool { return true },
})

s, err := ledger.OpenSession(
    netsec.Peer{NeighborAS: 65001, RouterID: "192.0.2.1"},
    uint64(1),
    netsec.Capabilities{GracefulRestart: true},
)
if err != nil {
    return err
}
if err := s.Announce(netsec.Route{ID: "r1", Prefix: "10.0.0.0/8", AttrsDigest: "sha256:..."}); err != nil {
    return err
}

_ = s.Disconnect(netsec.DisconnectTransport)
// stale can be forwarded until the deadline, but CanExport/CanFlowSpec are false.
_ = ledger.UpdatePolicy(2, func(r netsec.Route) bool { return false }) // immediately withdraws stale
```
