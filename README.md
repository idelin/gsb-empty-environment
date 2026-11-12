# CONNECT-UDP 目标地址准入

供 Go MASQUE 代理的 HTTP/3 请求处理器调用的、失败即拒绝的 RFC 9298
CONNECT-UDP 目标准入包。实现只使用 Go 标准库。

## 使用

```go
policy := netsec.NewAdmitter(
	net.DefaultResolver,
	netsec.AllowPrefixes(netip.MustParsePrefix("8.8.8.0/24")),
)

target, err := policy.AdmitRequest(r) // *http.Request from the H3 handler
if err != nil {
	http.Error(w, "target denied", http.StatusForbidden)
	return
}

// Dial only the fixed addresses. Never resolve target.Host again.
for _, dst := range target.AddrPorts() {
	_ = dst // dial the proxy's UDP socket to dst
}
```

数据平面在转发前调用 `netsec.UDPPayload(datagram)`。输入是 HTTP Datagram
Payload（QUIC DATAGRAM 中的 Quarter Stream ID 已由 HTTP/3 栈处理）；返回
`ok == false` 时（截断的 varint、非 0 Context ID 等）必须静默丢弃，不能
当作 UDP 负载。

## 安全边界

- 只接受 `/.well-known/masque/udp/{target_host}/{target_port}/`，要求端口
  为 `1..65535`，拒绝空主机/端口和多余路径段。
- 路径变量只做一次百分号解码；解码后若残留 `%`、`/`、`\`、`?`、`#`
  或控制字符即拒绝，阻止单次/多次编码绕过。
- 主机必须是 IP 字面量或 ASCII DNS 名。IP 字面量不经过 DNS。
- DNS 返回的**每个**地址都必须同时通过内置策略和调用方白名单；任意一个
  不合格就拒绝整个目标。`Admit` 只解析一次并返回固定 IP，调用方不得用
  主机名重解析后拨号，避免 DNS 重绑定窗口。
- 内置策略默认拒绝环回、链路本地、私有/ULA、共享、组播、未指定以及其他
  非全局单播地址；IPv4-mapped IPv6 会先还原为 IPv4 再检查。调用方白名单为
  `nil` 时拒绝所有目标。
- Context ID 使用 RFC 9000 QUIC varint 解码，只有 0 可承载原始 UDP 负载。

回归测试位于 `target_test.go` 和 `datagram_test.go`，覆盖编码绕过、DNS
多答案与重绑定、内网地址及 Context ID 0/非 0。
