# XMPP stanza来源与目标路由校验

XMPP 服务器转发 stanza 时，攻击者可在客户端流里伪造他人的 from，或让另一台服务器以未经认证的域名发 stanza。给Go XMPP 路由器写供 stanza 入口调用的校验包，按 RFC 6120 第 8 节：客户端流里 from 缺省时填入已绑定 JID，带 from 但与绑定的完整或裸 JID 不一致时以 invalid-from 流错误关闭；服务器间流里 from 的域名必须是该流上已认证（或已验证）的域名，否则同样以 invalid-from 关闭；服务器间流里 to 的域名不是本机托管域名时丢弃，不转发；绑定前收到的 stanza 一律丢弃。用冒用他人 JID、同一用户另一资源、未认证域名的 s2s、绑定前 stanza 验证。

## 使用

每个入站流维护一个 `Stream` 上下文，每个 stanza 进入路由器时调用
`Validate`。`Decision.Action` 决定后续动作：

- `ActionAccept`：可继续路由；若客户端原先没有 from，`Decision.Stanza.From`
  已填入绑定 JID。
- `ActionDrop`：静默丢弃，不转发、不回 stanza。
- `ActionCloseStream`：以 `Decision.Condition`（`invalid-from`）关闭流。

```go
client := netsec.NewClientStream("alice@example.org/desktop")
decision := client.Validate(netsec.Stanza{
    From: "bob@example.org/phone", // 伪造他人 JID
    To:   "carol@example.org",
})
if decision.Action == netsec.ActionCloseStream {
    // 发送 <stream:error><invalid-from/></stream:error> 并关闭流。
}

server := netsec.NewServerStream(
    []string{"example.org"},           // 本流已认证域名
    []string{"validated.example.net"}, // 已验证（如 dialback）域名
    []string{"example.org"},           // 本机托管域名
)
```

`Stream.ValidateXML` 可直接接收一个 stanza 的原始 XML。校验只负责来源与
目标路由判定；XML 解析失败、节流和实际网络 I/O 仍由调用方处理。
