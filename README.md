# MPLS入口标签栈准入

运营商边缘路由器的客户侧接口收到带标签的包，攻击者借此直接使用另一个VPN的标签进入其VRF。给Go转发面写供入口分类调用的标签准入包：非授信接口上，除调用方列出的运营商骨干标签外，带标签包一律丢弃；保留标签0到15默认拒绝（显式空标签仅在列出的接口允许），栈底位须与栈深一致，栈深超限和TTL为0拒绝；VPN标签只能经授信骨干接口解析到对应VRF。用客户端直接发VPN标签、保留标签、错误栈底位和合法骨干流量验证。

## 入口分类调用

```go
policy := netsec.Policy{
    MaxDepth: 4,
    VPNLabels: map[uint32]string{
        100: "vpn-blue",
        200: "vpn-red",
    },
    ExplicitNullInterfaces: []string{"ce-explicit-null"},
}
admitter := netsec.NewAdmitter(policy)

customer := netsec.NewUntrustedInterface("ce-1", 0x1234)
verdict := admitter.Admit(customer, []netsec.Label{
    {Value: 0x1234, BottomOfStack: true, TTL: 64},
})
if verdict.Dropped() {
    // 丢弃；verdict.Reason 给出拒绝原因
}

core := netsec.NewTrustedInterface("core-1")
verdict = admitter.Admit(core, []netsec.Label{
    {Value: 0x1234, BottomOfStack: false, TTL: 64},
    {Value: 100, BottomOfStack: true, TTL: 64},
})
// verdict.Allowed == true, verdict.VRF == "vpn-blue"
```

`stack` 按外层到内层排列。空栈表示未带标签的包，原样放行且不解析VRF。

## 准入规则

- 非授信接口：每个非保留标签都必须在 `Interface.BackboneLabels` 中；栈内出现任何已配置VPN标签立即丢弃，即使该标签误配进骨干标签列表。
- 保留标签：`0` 到 `15` 默认拒绝。仅当接口的 `AllowedReserved` 显式列出，或标签 `0` 出现在 `Policy.ExplicitNullInterfaces` 的接口名单中时才允许；标签 `0` 还必须是栈底标签。
- 栈结构：仅最后一个标签的 `BottomOfStack` 可为真，深度超过 `MaxDepth` 拒绝。零值 `MaxDepth` 使用 `DefaultMaxStackDepth`（8）。
- TTL：任一标签TTL为0即拒绝。
- VPN解析：只有 `Interface.Trusted == true` 的骨干接口才查询 `VPNLabels`。同一栈解析出不同VRF时按冲突拒绝。
