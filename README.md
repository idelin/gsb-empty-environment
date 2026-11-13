# NNTP按消息标识取文的组权限

新闻服务器按组限制读取，但 ARTICLE、HEAD、BODY 还能用 <message-id> 直接取文，攻击者不进入受限组也能读到其中的文章，XPAT、HDR、NEWNEWS 也可能跨组列出受限内容。给Go NNTP 服务写供命令处理调用的授权包：按消息标识取文时，文章的 Xref 头里至少有一个组调用者有读权限才返回，否则回 430，与不存在无法区分；HDR、XPAT 与 NEWNEWS 的结果只含调用者可读组中的文章，通配符展开前先过滤组；Xref 头在返回给调用者时去掉其无权读的组。用只存在于受限组的文章、跨发多组的文章、NEWNEWS 通配符验证，并核对回应时间与长度不区分无权与不存在。

## 命令处理接入

```go
auth := netsec.NewAuthorizer([]string{"public.*", "comp.lang.go"})

// ARTICLE / HEAD / BODY <message-id>
art, err := auth.LookupArticle(messageID, loadArticle)
if errors.Is(err, netsec.ErrNoSuchArticle) {
	// 无论文章不存在还是无权读取，都返回完全相同的 430 应答。
	fmt.Fprint(w, netsec.NoSuchArticleResponse())
} else if err != nil {
	// 存储层错误。
}
// art 中的 Xref 已仅保留调用者可读的组。

// HDR / XPAT
rows = auth.FilterHDR(currentGroup, rows)
rows = auth.FilterXPAT(currentGroup, rows)

// NEWNEWS <wildmat>
refs = auth.FilterNEWNEWS(wildmat, serverGroups, refs)
```

`NewAuthorizer` 同时接受精确组名和 wildmat 权限模式。`FilterNEWNEWS` 会先调用
`FilterGroups` 裁剪可用组，再展开通配符，因此 `*` 或 `secret.*` 不会暴露受限组。

## 验证

`go test ./...` 覆盖仅存在于受限组的文章、跨发多组文章、Xref 裁剪、HDR/XPAT
过滤、NEWNEWS 通配符，以及无权与不存在情况下 430 文本、长度和耗时的不可区分性。
