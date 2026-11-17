# SOCKS5 UDP关联来源绑定

SOCKS5代理的UDP ASSOCIATE会开放一个中继端口，攻击者在互联网上直接向该端口发包，就能让代理替自己对外发送或反射流量。给Go代理写供UDP中继循环调用的关联准入包：关联存活时间等于TCP控制连接；只转发来自请求声明的客户端地址（声明为0.0.0.0时取TCP连接来源地址）且首个端口固定后不再改变的数据报；FRAG非零、保留字段非零或域名长度越界直接丢弃；回程数据只送回该客户端，目标地址受调用方禁止网段限制。用外部伪造来源、FRAG包、端口漂移和控制连接断开后的发包验证。

## 用法

```go
policy := netsec.Policy{Forbidden: []*net.IPNet{
	loopback, private, linkLocal,
}}
association, err := netsec.NewAssociationWithPolicy(
	controlConn, // SOCKS5 TCP 控制连接
	requestAddr, // UDP ASSOCIATE 请求中的 BND.ADDR:BND.PORT
	policy,
)

for {
	n, src, err := relay.ReadFromUDP(buf)
	if err != nil {
		break
	}
	target, payload, err := association.Admit(src, buf[:n])
	if err != nil {
		continue // 伪造来源、坏头、端口漂移或禁止目标
	}
	sendToTarget(target, payload)
}
```

回程必须调用 `association.WriteReply(relay, remote, payload)`。该方法不接收客户端
目的地址，而是始终从关联内部取已固定的客户端地址，避免回程被引向其他主机。

`Association.Done()` 在 TCP 控制连接断开时关闭；此后 `Admit` 和 `WriteReply` 均返回
`ErrAssociationClosed`。请求端口为 0 时，首个通过完整校验的数据报固定 UDP 来源端口；
FRAG、RSV、地址头、域名长度或目标策略校验失败的数据报不会抢占该端口。
