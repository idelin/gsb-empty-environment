# Seatbelt末次匹配判定

macOS 沙箱配置以 (deny default) 起头，规则按出现顺序评估且最后一条匹配者生效；攻击者借 subpath、literal 与正则的覆盖差异，或后面的宽泛 allow 覆盖前面的 deny 读取用户私密目录。为Go评审组件实现供配置发布前调用的判定包：规则含 allow 或 deny、操作名（file-read*、file-write*、process-exec）与过滤器 literal、subpath、regex（整串匹配）；操作名以 * 结尾时通配该前缀的所有操作；给定操作与路径返回结果，并检出被后续规则完全遮蔽的 deny。用顺序翻转、subpath 边界（/a/b 不含 /a/bc）、通配操作和被遮蔽的 deny 验证。

## 用法

```go
rules := []netsec.Rule{
	netsec.NewRule(netsec.Deny, "file-read*", netsec.Subpath("/Users/alice/private")),
	netsec.NewRule(netsec.Allow, "file-read*", netsec.Subpath("/Users/alice")),
}

netsec.Evaluate(rules, "file-read-data", "/Users/alice/private/key")
// => allow：后一条 allow 覆盖了前一条 deny。

netsec.Evaluate(rules, "file-read-data", "/Users/alice/public/key")
// => allow

netsec.Evaluate(rules, "file-write-data", "/Users/alice/public/key")
// => deny：没有规则匹配，回落到 (deny default)。

netsec.ShadowedDenies(rules)
// => [0]：第 0 条 deny 被后续 allow 完全遮蔽。
```

`literal` 精确比较路径；`subpath` 包含目录自身及其后代，且要求组件边界，
因此 `/a/b` 不包含 `/a/bc`；`regex` 使用整串匹配。操作名末尾的 `*` 是前缀
通配，例如 `file-read*` 匹配 `file-read-data` 和 `file-read-metadata`。

`FindShadowedDenies` 返回详细报告，`ShadowedDenies`/`ShadowedDenyIndices`
返回被完全遮蔽的规则下标。只有后续 allow 覆盖 deny 的全部操作与路径域时，
该 deny 才会上报；这样它永远不会成为末条匹配规则，删除它不改变任何判定。
