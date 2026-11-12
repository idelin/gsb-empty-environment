# Ingress路径规范化与前缀边界

边缘代理按 Ingress 的 Exact 与 Prefix 路径规则选后端，攻击者发 /admin/../public、//admin、/admin%2Fx 或 /ADMIN 绕过只给内部服务的 /admin 前缀。为Go代理实现可嵌入的路径匹配包：匹配与转发使用同一份规范路径，Prefix 按元素边界匹配（/foo 命中 /foo/bar 不命中 /foobar），点段先消去再匹配，含 %2F、%5C 或解码后含 NUL 的请求拒绝，大小写敏感，多条规则命中时 Exact 优先、再取最长 Prefix。用上述路径验证，并断言转发路径与匹配路径一致。

## 用法

```go
router, err := netsec.NewRouter(
	netsec.Rule{Kind: netsec.Exact, Path: "/admin", Target: "admin"},
	netsec.Rule{Kind: netsec.Prefix, Path: "/admin", Target: "admin-api"},
	netsec.Rule{Kind: netsec.Prefix, Path: "/", Target: "public"},
)
if err != nil {
	// 规则配置错误
}

// 必须使用 EscapedPath：URL.Path 已经把 %2F 解码成 /。
match, err := router.Match(r.URL.EscapedPath())
if err != nil {
	http.Error(w, "invalid path", http.StatusBadRequest)
	return
}

target := "public"
if match.Matched {
	target = match.Rule.Target
}
forward(target, match.Path) // 不能转发 r.URL.Path 或原始请求路径
```

## 语义

- 匹配和转发都使用 `CanonicalPath` 的结果，即 `Match.Path`。
- 请求路径先单次百分号解码，再消去 `.`/`..` 段并折叠重复斜杠。
- 含 `%2F`、`%5C`、编码 NUL（包括大小写变体）或二次编码分隔符的路径返回 `ErrPathRejected`，调用方应直接拒绝请求。
- Prefix 按 `/` 元素边界匹配：`/foo` 命中 `/foo`、`/foo/bar`，不命中 `/foobar`。
- 路径匹配大小写敏感。Exact 优先于任何 Prefix；多个 Prefix 命中时取最长者。
- `Match.Path` 始终是重新转义后的规范路径；即使没有规则命中也会返回，供默认后端安全转发。
