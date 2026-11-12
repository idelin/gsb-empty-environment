# NNTP取消消息授权

新闻服务器接收的文章带Control头，攻击者借折叠头里的换行注入伪造的cancel，或取消他人文章。给Go服务器写供POST处理调用的控制消息授权包：头部折叠后重新规范化，重复的Control和出现在折叠续行里的Control拒绝；cancel与supersedes必须带Cancel-Key，按RFC 8315的方案计算哈希后须出现在原文章的Cancel-Lock中，原文章没有Cancel-Lock时只允许调用方配置的管理员发送者；Path已含本站标识的文章视为回环丢弃。用折叠注入、错误密钥、无锁原文和合法取消验证。

## POST 集成

```go
auth, err := netsec.NewAuthorizer(netsec.Config{
	SiteID:       "news.local.example",
	AdminSenders: []string{"moderator@example.com"},
}, netsec.LookupFunc(func(messageID string) (netsec.Header, bool, error) {
	// 从文章库读取原文章的规范化头部。
	return storedHeaders(messageID)
}))
if err != nil {
	log.Fatal(err)
}

decision, err := auth.AuthorizeAs(rawArticle, authenticatedPoster)
if err != nil {
	// 存储/查询故障，不能继续投递。
	return err
}
switch decision.Disposition {
case netsec.Deliver:
	// 普通文章，入库。
case netsec.ApplyControl:
	// 已授权的 cancel 或 supersedes，按 decision.Target 执行。
case netsec.Reject:
	// 拒绝文章；decision.Reason 可用于日志或 NNTP 错误响应。
case netsec.Drop:
	// Path 表明文章已经过本站，直接丢弃，避免回环。
}
```

`AuthorizeAs` 的第二个参数是 POST 处理层已经认证的发送者身份。没有认证身份时可调用
`Authorize(rawArticle)`，此时无 `Cancel-Lock` 原文的管理员判定只能依赖文章 `From`
头；生产环境应优先传入认证身份，避免伪造 `From`。
