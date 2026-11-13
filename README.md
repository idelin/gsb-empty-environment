# VPN断连窗口的泄漏封堵状态机

设备在 VPN 重连、切换网络或隧道意外断开的间隙，如果放行默认路由，应用会通过明文网络发出请求并泄露 DNS，攻击者只需制造隧道断线。为Go终端代理写可嵌入网络变更回调的泄漏封堵状态机，状态为 connecting、up、reconnecting、switching_network、disconnected_by_user，事件含隧道状态变化、物理网络变化、用户操作与超时，输出是应用到过滤层的规则集（允许的目的地址与接口）：up 时只放行隧道接口，以及物理接口上到 VPN 服务器地址的流量；connecting、reconnecting 与 switching_network 时只放行到 VPN 服务器的握手流量，DNS 查询一律丢弃，不得回退到系统解析器；规则替换必须原子，新规则集生效前旧规则集不得撤销，替换失败时保持全丢弃；只有 disconnected_by_user 才放行直连；局域网放行取决于调用方开关，打开时也不放行 VPN 服务器地址之外的公网；网络切换后服务器路由可能变化，旧物理接口的放行在切换事件到达时立即撤销。用重连窗口 DNS、切换网络、规则替换失败、用户断开和局域网开关验证每个状态下的规则集。

## 接入

`RuleReplacer.Replace` 必须由过滤层实现为原子替换：新规则可见前保留旧规则，
返回错误时不得移除旧规则。状态机在替换失败后立即提交 `FailClosed` 的全丢弃
规则集；连续失败时 `Ruleset()` 仍报告全丢弃。

```go
filter := myFilter{} // 实现 RuleReplacer
m, err := netsec.New(netsec.Config{
		TunnelInterface:    "utun0",
		PhysicalInterfaces: []string{"en0"},
		VPNServers:         []netip.Addr{netip.MustParseAddr("203.0.113.10")},
		LANEnabled:         false,
}, filter)
if err != nil {
		// m 若返回则已处于 fail-closed，可直接用于后续事件。
}

// 网络/隧道回调
_ = m.TunnelChanged(netsec.TunnelUp)
_ = m.NetworkChanged(netsec.NetworkChange{
		PhysicalInterfaces: []string{"en1"},
		VPNServers:         []netip.Addr{netip.MustParseAddr("203.0.113.20")},
})
_ = m.OnUserAction(netsec.UserDisconnect)
_ = m.Timeout()

// 运行时局域网开关
_ = m.SetLANEnabled(true)
```

`NetworkChange.PhysicalInterfaces` 是完整快照，因此切换事件一到达就会替换旧接口
集合；`VPNServers` 为 `nil` 时沿用旧地址，非 `nil` 空切片则清空。严格重连窗口内
`Ruleset.DNS == DNSDeny`，`up` 使用 `DNSTunnel`，只有 `disconnected_by_user`
使用 `DNSDirect`。

运行验证：

```sh
go test ./...
```
