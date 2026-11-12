# VRRP通告来源与优先级准入

攻击者从外部网络发优先级255的VRRP通告，想抢占主路由器。给Go路由守护进程写供收包路径调用的VRRPv3通告准入包：IP TTL须为255、目的为224.0.0.18，校验和含伪首部，VRID与本机配置一致；优先级255只接受源地址属于虚拟地址拥有者的报文；Adver Interval与本机不符的通告丢弃并计数。用TTL小于255、伪造拥有者和间隔不符验证。

## `vrrp` 包

`vrrp` 子包实现 RFC 5798（VRRPv3）IPv4 通告的收包准入，入口为
`Admitter.Admit`，供收包 goroutine 直接调用，并发安全（丢弃原因用
`atomic.Uint64` 计数）。

准入顺序与规则：

1. 报文长度、版本（v3）、类型（ADVERTISEMENT）
2. IP TTL 必须为 255（`ReasonTTL`），阻断跨路由器的外部发送者
3. 目的地址必须为 224.0.0.18（`ReasonDestination`），源须为合法单播 IPv4
4. VRID 与本机配置一致（`ReasonVRID`）
5. VRRP 校验和按 RFC 5798 §5.2.8 覆盖 IPv4 伪首部 + VRRP 报文
   （`ReasonChecksum`），伪造源/目的地址会导致校验失败
6. 地址计数与长度一致，通告的虚拟地址集合与本机配置一致
7. Max Advertisement Interval 与本机一致，不符丢弃并计数
   （`ReasonMaxAdvertInterval`）
8. 优先级 255 仅当源地址属于本机配置的虚拟地址（IP 地址拥有者）时接受，
   否则按伪造拥有者丢弃（`ReasonOwnerSpoof`）

调用示例：

```go
adm, err := vrrp.NewAdmitter(vrrp.Config{
    VRID:                     42,
    MaxAdvertisementInterval: 100, // 厘秒
    VirtualAddresses:         []netip.Addr{netip.MustParseAddr("10.0.0.1")},
})
if err != nil {
    return err
}

adv, reason := adm.Admit(vrrp.Inbound{
    TTL:         ipHeader.TTL,
    Source:      srcAddr,
    Destination: dstAddr,
    VRRPPacket:  ipPayload,
})
if reason != vrrp.ReasonNone {
    metrics.VRRPDrops.WithLabelValues(reason.String()).Inc()
    return // 丢弃
}
// 处理 adv
```

测试：`go test ./...`（含 `-race`），覆盖 TTL<255、优先级 255 伪造拥有者、
通告间隔不符、伪首部校验和等场景。
