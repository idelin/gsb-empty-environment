# SOME/IP服务发现伪造防护

SOME/IP-SD里攻击者发伪造Offer抢占服务，或让Subscribe的端点选项指向受害者以反射流量。给Go车载网关写供SD报文处理调用的准入包：Offer只接受调用方登记的服务ID、实例、主版本对应的提供者地址，TTL为0的StopOffer只能来自当前提供者；Subscribe端点选项地址须等于报文源地址，选项索引越界的条目丢弃。用抢占Offer、伪造StopOffer和反射订阅验证。

## 模块

包名 `netsec`，无第三方依赖，仅使用标准库。

- `types.go`：SOME/IP-SD 报文、Entry、Option 的解码类型与常量。
- `wire.go`：`ParseMessage`，按 AUTOSAR FO PRS 线格式解析完整 SOME/IP-SD 数据报（16 字节 SOME/IP 头 + 12 字节 SD 头 + entries/options），对长度、保留位、选项边界做校验。
- `admit.go`：`Controller` 准入控制与按条目的 `Verdict`（`Accept`/`Reject`/`Drop`）及拒绝原因。

## 准入策略

- **Offer 防抢占**：`RegisterProvider(ServiceKey, 地址)` 登记每个 `(ServiceID, InstanceID, MajorVersion)` 允许的提供者。Offer 的报文源地址不在登记集合内即 `Reject`；元组未登记返回 `ReasonServiceNotRegistered`，已登记但源地址不匹配返回 `ReasonProviderNotAllowed`。Offer 自带的单播端点选项地址也必须等于报文源地址。
- **StopOffer 防伪造**：OfferService 且 TTL=0 视为 StopOffer，只有当前持有该元组的提供者地址发来的才接受，并清除在线状态；否则返回 `ReasonStopOfferFromNonProvider`。
- **订阅防反射**：Subscribe 第一选项运行中的每个单播端点（含 SD 端点）选项地址必须等于报文源地址，否则 `Reject`（`ReasonEndpointSourceMismatch`）。组播选项承载组地址，不参与该校验。
- **越界条目丢弃**：任一选项运行的索引/数量超出 options 数组，该条目 `Drop`（`ReasonOptionIndexOutOfBounds`），不改动任何状态，也不影响同报文内其他条目。

## 调用示例

```go
ctl := netsec.NewController()
ctl.RegisterProvider(netsec.ServiceKey{
    ServiceID: 0x1234, InstanceID: 0x0001, MajorVersion: 0x02,
}, netip.MustParseAddr("10.0.0.10"))

// source 取自套接字的对端地址，报文则是 UDP 收到的完整 SOME/IP-SD 数据报。
verdicts, err := ctl.AdmitPacket(datagram, source)
if err != nil {
    // 线格式错误：丢弃整包并计入畸形流量统计。
    return
}
for i, v := range verdicts {
    switch v.Verdict {
    case netsec.VerdictAccept:
        sdStateMachine.Apply(msg.Entries[i])
    case netsec.VerdictReject:
        log.Warn("SD entry rejected", "reason", v.Reason)
    case netsec.VerdictDrop:
        log.Warn("SD entry dropped", "reason", v.Reason)
    }
}
```

## 验证

`go test -race ./...` 覆盖三类攻击及线格式边界：

- `TestSquattingOfferRejected`：攻击者 Offer 被拒且不会成为当前提供者，合法提供者上线后再次抢占仍被拒。
- `TestForgedStopOfferRejected`：伪造 TTL=0 StopOffer 被拒、服务不下线；当前提供者自己的 StopOffer 才生效。
- `TestReflectionSubscribeAndOutOfBounds`：端点指向受害者的订阅被拒；选项索引越界（含第二选项运行）的条目被 Drop；源地址一致的正常订阅放行。
- 另含解析往返、畸形报文、Offer 端点不一致、注销提供者、组播选项、并发访问等用例。
