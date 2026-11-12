# DHCP中继链授权

DHCP中继插入的端口身份可能被客户端预先伪造。实现供Go接入宿主调用的中继封装包，客户端来源的中继字段拒绝，可信中继链逐跳绑定入端口、事务和客户身份；服务器回复必须沿原链返回，移除字段时验证确为本跳添加。用嵌套中继和同事务号客户测试身份不串用。

## 设计

- 报文格式遵循 RFC 8415（DHCPv6）的 RELAY-FORW/RELAY-REPL、
  Relay-Msg（选项 9）与 Interface-ID（选项 18），仅依赖标准库。
- 入向侧：`RelayAgent.WrapClient` 只接受普通客户端消息，发现中继类型
  或中继专属选项即返回 `ErrClientRelayForged`，客户端无法预植端口身份。
- 中继侧：`RelayAgent.WrapRelay` 先验证上游在可信对等体清单内，再以
  “事务号 + 客户身份（对端地址/DUID）+ 本跳入端口 + 跳数”为键记录绑定。
- 链路校验：`HOP_COUNT` 必须逐跳严格递减并在边缘归零，每跳 Interface-ID
  唯一非空，对端地址不得为全零，异常返回 `ErrChainCorrupt`。
- 服务器侧：`Server.Unwrap` 恢复完整链路与事务上下文；同一事务号下不同
  客户身份独立保存，键相同但链路冲突时返回 `ErrBindingMismatch`。
- 回复侧：`Server.BuildReply` 按入向链路逆序、原跳数、原端口身份构造
  RELAY-REPL；`RelayAgent.UnwrapReply` 命中本跳绑定后才剥离本跳字段，
  确保移除的 Interface-ID 确为本跳添加，回复只能沿原链返回。

## 用法

```go
edge := netsec.NewRelayAgent(nil)
core := netsec.NewRelayAgent([]string{"::1"})

forward, err := edge.WrapClient(solicit, netsec.PortIdentity{
    Device: "edge", Interface: "ge-0/0/1",
}, linkAddr, clientAddr)
forward, err = core.WrapRelay(forward, netsec.PortIdentity{
    Device: "core", Interface: "xe-1/0/0",
}, coreLink, peerAddr)

tx, err := netsec.NewServer().Unwrap(forward)
reply, err := server.BuildReply(tx, dhcpReply)

toEdge, err := core.UnwrapReply(reply)
toClient, err := edge.UnwrapReply(toEdge)
```

## 验证

```sh
go test -race ./...
```

测试覆盖客户端伪造字段拒绝、两级嵌套中继、同事务号不同客户的身份隔离、
回复端口身份篡改、非可信对等体与跳数篡改等场景。
