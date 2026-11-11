# dhcpsnoop

接入交换机的 DHCP 监听（DHCP Snooping）、动态 ARP 检查（DAI）与 IP 源保护（IPSG）组件。
所有时间由虚拟时钟驱动，便于用确定性的报文序列逐包核对放行/丢弃。

## 信任模型

- `New(clock, uplink...)` 中登记的端口为信任端口（上联口），其余均为非信任接入端口。
- OFFER / ACK / NAK 只允许从信任端口进入；接入端口上的伪造服务器应答一律丢弃
  （`untrusted-server-reply`），且不改变任何状态。
- 接入端口上的 DISCOVER / REQUEST / INFORM 按 XID 记录事务（客户端 MAC + 端口 +
  过期时间）；信任端口上的客户端方向报文直接旁路。

## 绑定表

只有满足以下全部条件的 ACK 才会写入绑定表（`IP -> MAC, Port, Expiry`）：

1. 来自信任端口；
2. XID 能匹配到本机接入端口上的活跃事务，且 ACK 的 `chaddr` 与事务 MAC 一致；
3. 携带非零的 `yiaddr` 与正数租约时间。

表项在三种情况下删除：

- 租约到期：每次收包惰性清理，也可由定时器调用 `Sweep()` 主动清理；
- 合法 RELEASE：IP、MAC、端口三者与表项匹配后立即删除；
- 合法 DECLINE：规则同 RELEASE。

INFORM 的 ACK 不携带租约，只校验事务，不产生绑定表项；NAK 不产生绑定，
并终结对应事务。

## 端口迁移与重复 ACK 的确定结果

- 同一 MAC 在新端口重新完成 DHCP：ACK 命中同一 IP 的既有表项且 MAC 相同，
  绑定迁移到事务记录的新端口，旧端口随后的 ARP/IP 报文返回 `port-mismatch`。
- 重复 ACK（同 XID 或事务已老化的新 XID）：MAC 与既有表项一致则刷新租约、
  表项数量不变；MAC 不一致返回 `binding-conflict`，原表项保持不变。
- 未知 XID 且该 IP 无绑定的 ACK 返回 `transaction-unknown`，不会凭空建表。

## ARP 检查与 IP 源检查

`HandleARP` 与 `HandleIP` 查询同一张绑定表，按 `IP → 端口 → MAC` 顺序匹配，
失败原因分别为 `no-binding` / `port-mismatch` / `mac-mismatch`：

- 发送方 IP 为 0.0.0.0 的 ARP 探测（RFC 5227）放行，不建表；
- 信任端口的 ARP/IP 流量旁路；
- 源地址全零等畸形报文返回 `invalid-message`。

## 虚拟时钟

`VirtualClock` 实现 `Clock` 接口，支持 `Advance(d)` 与 `Set(t)`，
零值起点为 Unix epoch。租约到期与事务 TTL（默认 10 分钟，可用
`SetTransactionTTL` 调整）都读取该时钟。

## 最小示例

```go
clock := NewVirtualClock(time.Unix(1_700_000_000, 0).UTC())
s := New(clock, 0) // 端口 0 为上联口

s.HandleDHCP(DHCPPacket{InPort: 1, XID: 1, Type: DHCPRequest, Chaddr: mac})
s.HandleDHCP(DHCPPacket{InPort: 0, XID: 1, Type: DHCPAck, Chaddr: mac,
	Yiaddr: ip, Lease: time.Hour})

r := s.HandleIP(IPPacket{InPort: 1, SrcMAC: mac, SrcIP: ip})
// r.Allowed() == true

clock.Advance(time.Hour)
s.Sweep() // 租约到期，绑定被删除
```

每个报文的判定结果为 `Result{Action, Reason}`，测试用例逐包断言
ALLOW/DROP 及具体原因，并核对绑定表的最终状态。运行：

```sh
go test ./...
```
