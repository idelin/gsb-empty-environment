# 递归解析器缓存窥探防护

外部主机用 RD=0 的查询探测解析器缓存里有哪些名字，借此判断内部用户访问了什么。为Go递归解析器写可导入的查询准入包：只为登记网段的客户端服务，其余来源一律 REFUSED；登记网段内 RD=0 的查询只按本地权威区数据回答，不看缓存；REFUSED 响应不携带任何缓存数据；缓存命中与否对外不得影响应答的 TTL 值（一律给配置的整数值）。用 RD=0 探测、外部 RD=1 和内部正常查询验证。

## 用法

包只依赖 Go 标准库。策略在解析之前检查客户端地址；`RD=0` 分支不会调用
`Cache` 或 `Upstream`。

```go
policy, err := netsec.NewPolicyFromCIDRs(
	[]string{"10.0.0.0/8", "2001:db8::/32"},
	60, // 所有对外记录统一使用该 TTL
)
if err != nil {
	log.Fatal(err)
}

record, _ := netsec.ARecord(
	"www.internal.example", netip.MustParseAddr("10.0.0.1"),
)
zone, err := netsec.NewZone("internal.example", record)
if err != nil {
	log.Fatal(err)
}

handler, err := netsec.NewHandler(netsec.HandlerConfig{
	Policy: policy,
	Zones:  []*netsec.Zone{zone},
	Cache:  cache,
	Upstream: upstream,
})
if err != nil {
	log.Fatal(err)
}

// client 必须来自传输层（例如 UDP 的远端地址），不能信任报文内容。
reply, err := handler.HandlePacket(queryWire, clientAddr)
```

也可以直接使用已解码的请求：

```go
response := handler.Resolve(ctx, netsec.Request{
	Client:           clientAddr,
	Question:         netsec.Question{Name: "www.internal.example", Type: netsec.TypeA, Class: netsec.ClassINET},
	RecursionDesired: true,
})
```

## 行为保证

- 未登记来源发出的任意查询（包括 `RD=0` 和 `RD=1`）返回 `REFUSED`，且
  answer/authority/additional 三个区都为空。
- 登记来源的 `RD=0` 查询只查本地权威区；命中返回权威数据，区内不存在则返回
  `NXDOMAIN`/`NODATA`，区外名称直接 `REFUSED`，不会查缓存。
- 登记来源的 `RD=1` 查询依次检查本地权威区、缓存、上游递归解析。
- 所有输出记录（包括缓存、上游和负响应的 SOA）的 TTL 都被统一替换为策略中
  配置的整数，缓存命中与未命中在外部不可区分。

`go test ./...` 覆盖内部 `RD=0` 探测、外部 `RD=1`、内部正常递归查询、负响应和
DNS 线格式响应。
