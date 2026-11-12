# TAP二层控制帧过滤

租户容器能向宿主的TAP口写任意以太帧，攻击者借此发送STP配置包争夺根桥，或发送PAUSE帧冻结物理口。给Go虚拟交换机写供TAP读取循环调用的二层控制帧过滤包：目的地址在01:80:C2:00:00:00到0F的保留组播一律丢弃；类型长度字段小于0x0600的帧只在长度与实际载荷一致时放行且默认不转发；外层带多层VLAN标签的帧丢弃；TUN模式先按是否带包信息头解析，版本半字节与配置的地址族不一致拒绝。用BPDU、PAUSE、长度字段不符、QinQ和缺失包信息头的帧验证。

## 用法

过滤器无状态、零分配，可在 TAP/TUN 读取循环中对每个读出的报文调用：

```go
f := netsec.New(netsec.Config{Mode: netsec.ModeTAP})
v := f.Frame(frame)
switch v.Action {
case netsec.ActionDrop:
    // 计入丢弃统计后直接跳过，v.Reason 给出具体原因
case netsec.ActionLocal:
    // 802.3 LLC 帧仅上交本机协议栈，不向其他端口 flood
default:
    switchPort.Forward(v.Payload)
}
```

TUN 模式通过 `Config{Mode: ModeTUN, PacketInfo: true, Family: FamilyIPv4}`
开启包信息头（tun_pi）校验；设置 `IFF_NO_PI` 时将 `PacketInfo` 置为 false，
过滤器直接按 IP 首字节版本半字节核对地址族。

## 过滤规则

- 目的 MAC 命中 `01:80:C2:00:00:00/45`（末字节 0x00–0x0F）一律丢弃，
  覆盖 STP BPDU（…:00）、802.3 PAUSE（…:01）、LACP（…:02）等控制帧。
- 类型/长度字段 < `0x0600` 时按 802.3 长度字段处理：声明长度与实际载荷
  （TAP 帧不含 FCS）不一致丢弃；一致则 `ActionLocal` 放行但默认不转发。
- 外层出现两层及以上 VLAN 标签（`0x8100`/`0x88A8`/`0x9100`，即 QinQ）丢弃；
  单层标签剥除后继续检查内层类型。
- TUN 带包信息头时先校验 4 字节 tun_pi（缺失/截断拒绝，PI proto 与配置
  地址族不符拒绝），再校验 IP 版本半字节；无 PI 时直接校验版本半字节。
