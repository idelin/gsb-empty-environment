# 邮件认证结果信任边界

入站邮件可伪造Authentication-Results头，业务服务不能直接信任最上方的pass。写供Go邮件接入宿主调用的清洗包，可信结果只来自当前接收链的受信节点证明，外部同名头完整保留为不可信证据；按节点顺序绑定来源和认证方法，重复或断链拒绝提升可信等级。用真实头部解析与注入链证明测试插入伪头、转发节点换名及同一邮件不同接收连接不能复用证明。

## 用法

```go
// 每个接收连接新建证明链，受信节点按投递顺序逐一证明自己所加的头部。
chain := netsec.NewChain(connID)
chain.Attest("mx1.example.org", rawAddedHeader, "spf", "dkim")
chain.Attest("mx2.example.org", rawAddedHeader2, "dkim", "spf")

// 同一封邮件换一个连接接收时，旧链证明一律失效，必须重新 Attest。
res := netsec.Sanitize(msg.Header, chain, connID)
if spf, ok := res.MethodResult("spf"); ok { /* 仅来自受信节点 */ }
for _, ev := range res.Untrusted { /* 外部同名头原文留证 */ }
```

运行测试：`go test ./...`
