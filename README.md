# redact — 流式日志脱敏库

按词典做最长匹配替换，支持词典版本管理与流式输入，输出替换结果及来源映射。

## 匹配语义

- **最长匹配**：每个输入位置取能匹配的最长词条。
- **ID 决胜**：同一词条注册在多个 ID 下（等长冲突）时，小 ID 胜出。
- **不重叠**：匹配发出后，扫描从其末尾继续。
- **流式提交**：只有当一段输入已不可能被后文延长（即剩余后缀不再是任何词条的真前缀）时才提交输出；否则保留在未决缓冲中。`Flush` 按流结束处理，强制提交全部未决内容。
- **版本边界**：`SetVersion` 从明确的字节边界生效——边界前已写入的字节（包括仍在未决缓冲中的）仍受旧词典管辖；一个匹配由它起始位置处的词典版本判定。
- **背压**：`WithMaxPending(n)` 限制未决字节数，超限时 `Write` 返回 `ErrBackpressure` 且不消费任何数据；待未决片段被后续输入消解或 `Flush` 强制提交后可重试。

## 用法

```go
vd := &redact.VersionedDict{}
vd.Add(1, redact.Entry{ID: 1, Word: "secret", Replacement: "[S]"})
vd.Add(2, redact.Entry{ID: 2, Word: "token", Replacement: "[T]"})
vd.Remove(3, 1)

r := redact.NewRedactor(vd.At(1), redact.WithMaxPending(1<<20))
r.Write([]byte("the secret "))
r.SetVersion(vd.At(2))          // 从下一个字节起换版
r.Write([]byte("token here"))
r.Flush()

out := r.Output()               // 替换后的输出
for _, seg := range r.Mapping() // 输出区间 -> 输入区间（含词条 ID）
```

## 测试

```sh
go test ./...
```

覆盖：词条互为前缀（整块/分片/分叉字节）、等长 ID 决胜、不重叠、换版边界（两侧各自版本、未决字节保留旧版）、超长未决片段背压、来源映射完整性与流式/一次性结果一致性。
