# 多播名称应答准入

企业终端上的局域网名称解析会被攻击者抢答，使WPAD之类的名字解析到恶意主机。给Go解析器写供多播应答入口调用的准入包：mDNS应答只在源端口为5353、IP TTL为255、来源在本机同一前缀内且名称位于.local下时接受；LLMNR应答必须匹配未完成查询的事务号并来自本链路；wpad和isatap不允许经多播解析；附加段里与问题无关的记录丢弃，不入缓存。用跨网段来源、TTL为254、wpad应答、附加记录投毒和合法.local应答验证缓存。

## 包 `netsec`

`Admitter.Admit(meta, msg)` 是供多播应答 socket 入口调用的唯一准入函数，
返回 `Decision{Accepted, Reason, Cached}`。被拒包不写任何缓存。

### 准入规则

- **mDNS（RFC 6762）**，全部满足才接受：
  - UDP 源端口为 5353（`ReasonMDNSSourcePort`）；
  - IP TTL / IPv6 跳数为 255，任何更低值都意味着经过路由（`ReasonMDNSPacketTTL`）；
  - 源地址落在本机接口 on-link 前缀，或属于链路本地地址 `169.254.0.0/16`、`fe80::/10`
    （`ReasonMDNSOffLink`）；
  - 报文内所有名称都在 `.local` 特殊域下（`ReasonMDNSNotLocal`）。
- **LLMNR（RFC 4795）**：
  - UDP 源端口为 5355；
  - 源地址在本链路（`ReasonLLMNROffLink`）；
  - 事务号与问题名必须匹配一个仍未超时的未完成查询（`RegisterPending`，
    `ReasonLLMNRNoTransaction`）；接受后该事务即被消费，重放应答同样被拒。
- **名称策略**：任意段（问题/回答/授权/附加）中出现首标签为 `wpad` 或
  `isatap` 的名称一律拒绝（`ReasonBlockedName`），大小写不敏感。
- **附加段净化**：只有从问题名出发、沿 CNAME/PTR/SRV/MX/NS/SOA 等
  名称型 RDATA 可达的记录才入缓存；与问题无关的附加记录（典型投毒载荷）
  一律丢弃。地址类与 TXT 记录不引入新名称，不能扩大可达集合。

### 缓存

- 键为规范化名称（小写、补尾点），记录按 DNS TTL 存绝对过期时间，
  查询时惰性清除；TTL 为 0 的 mDNS goodbye 会驱逐旧条目而非入缓存。
- `Lookup(name, rtype)` 只返回未过期记录；`CachedNames()` 用于测试与巡检。
- 时钟经 `NewAdmitter(links, now)` 注入，便于确定性测试；生产环境传 `time.Now`。

### 入口示例

```go
links := LinkConfig{Prefixes: ifacePrefixes} // 来自接口配置的 on-link 前缀
adm := NewAdmitter(links, time.Now)
adm.RegisterPending(PendingQuery{Proto: LLMNR, ID: txid, Name: qname, ExpiresAt: deadline})

dec := adm.Admit(PacketMeta{
    Proto: MDNS, SourceIP: srcAddr, SourcePort: srcPort,
    IPTTL: ipTTL, InterfaceID: ifi,
}, parsedMsg)
if !dec.Accepted {
    metrics.Inc("multicast_reject:" + dec.Reason)
    return
}
// dec.Cached 即实际写入缓存的记录，无关附加段已被剔除。
```

### 验证

`go test ./...` 覆盖：合法 `.local` 接受、跨网段来源拒绝、TTL 254 拒绝、
wpad/isatap 应答（含藏在附加段）拒绝、附加记录投毒丢弃而 CNAME 链保留、
LLMNR 事务号匹配/超时/重放、缓存 TTL 过期与 goodbye 驱逐。
