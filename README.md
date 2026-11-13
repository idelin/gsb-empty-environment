# 单播反向路径转发的严格松散与可行模式

攻击者伪造源地址发包，让服务把大流量反射给受害者，或穿过边界进到内网。为Go路由转发面写可导入的 uRPF 判定包：严格模式要求到源地址的最佳路径出接口等于入接口；可行模式只要任一可行路径出接口为入接口；松散模式只要有任意非黑洞路由，默认路由仅在配置允许时算数；等价多路径全部考虑。用非对称路由、仅默认路由、黑洞路由和等价多路径验证。

## 用法

```go
import (
	"net/netip"

	netsec "example.org/netsec/q0734"
)

routes := []netsec.Route{
	{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Egress: "eth0"},
	{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Egress: "eth1"},
	{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Egress: "wan"},
}

table, err := netsec.NewTable(routes, netsec.Config{
	AllowDefaultForLoose: false,
})
if err != nil {
	// 路由表非法，例如非黑洞路由缺少出接口。
}

source := netip.MustParseAddr("192.0.2.55")
decision := table.Check(source, "eth1", netsec.ModeStrict)
if !decision.Allowed {
	// 丢弃伪造源地址报文；decision.Reason 可用于日志和指标。
}
```

`Table` 构造后不再修改，可安全地被多个转发协程并发调用。判定热路径不分配切片。

## 判定语义

- `ModeStrict`：先取覆盖源地址的最长前缀，再在该前缀内取最小 `Metric`；所有同前缀、同度量的路径都属于最佳路径，任一非黑洞路径的出接口等于入接口即通过。度量值更大的路径不会参与最佳路径比较。
- `ModeFeasible`：扫描所有覆盖源地址的非黑洞路由，不要求是首选路径或最低度量；任一路径的出接口等于入接口即通过。
- `ModeLoose`：存在任一覆盖源地址的非黑洞路由即通过，不比较入接口。普通路由始终算数；`/0` 默认路由仅在 `Config.AllowDefaultForLoose` 为 `true` 时算数。
- 黑洞路由永远不能作为返回路径。严格模式下若最佳路径只包含黑洞表项则拒绝；可行/松散模式仍可被其它非黑洞的覆盖路由满足。
- 源地址无效、未指定或为组播时直接拒绝；IPv4-mapped IPv6 地址在查表前解映射为 IPv4。路由前缀在构造时做 `Masked()` 归一化。

## 验证

```sh
go test ./...
go vet ./...
```
