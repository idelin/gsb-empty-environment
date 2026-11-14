# SIP路由头把响应带去了错误的对端

SIP代理按请求里的Route与Via转发，攻击者伪造这些头让响应或后续请求发往自己，从而截获认证信息。请实现供Go SIP代理调用的可导入路由校验组件。只信任本域记录的路由集，Via的branch、sent-by与received按逐跳规则核对，Route目标必须在信任域内；记录路由由代理自身追加，客户端新增项不改变实际下一跳。对话标识、标签与CSeq按事务状态校验，跨域请求走显式信任策略。用伪造Route、Via改写、branch复用、跨域转发和合法对话验证响应只回到真实对端。

## 组件用法

```go
v := netsec.NewValidator(netsec.Config{
    OwnDomains:   []string{"proxy.example.com"}, // 本域
    TrustedPeers: []string{"peer.example.net"},  // 显式跨域信任策略
})

// 收到请求时：SrcAddr/SrcPort 必须取自传输层连接，不能取自头部。
dec, err := v.ValidateRequest(msg)
// dec.NextHop   安全下一跳（仅来自本域记录的路由集与可信 Route）
// dec.RespHost/RespPort 响应回送地址（观测到的真实对端）

// 代理自身追加 Record-Route 时登记：
v.RecordOwnRoute(callID, ownRoute)

// 响应回程时按事务表核对并取回真实对端地址：
host, port, err := v.ValidateResponse(resp)
```

- `sip.go`：SIP URI / Via / 消息模型与解析。
- `validator.go`：逐跳 Via 核对（branch magic cookie、sent-by 与 received 对观测对端）、Route 信任域校验、代理自记录路由集、对话标签与 CSeq 状态校验、跨域显式信任策略。
- `validator_test.go`：伪造 Route、Via 改写、branch 复用、跨域转发与合法对话的验证。
