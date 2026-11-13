# 连接符上下文

ZWNJ出现在不允许连接的字母之间，标签被注册。ZWNJ和ZWJ只有在规定的连接上下文中才允许；其他位置失败。满足上下文的连接符保留。主机名调用可导入的Go解码器做这次裁决。在两个拉丁字母之间插入ZWNJ。

## API

`netsec.Decode`, `netsec.DecodeHostname`, and `netsec.Hostname` decode an
A-label (or validate a U-label) and apply the RFC 5892 Appendix A ContextJ
rules. Invalid joiner placement returns an error. Joiners that satisfy the
joining context are retained in the decoded result.

```go
decoded, err := netsec.DecodeHostname("xn--mgbh0fb")
```

The `example.org/netsec/q0324/decoder` package exposes the same decoder for
callers that want to import it directly:

```go
decoded, err := decoder.DecodeHostname(host)
```

`netsec.Registration` is the strict profile used for registration checks.
`netsec.ToASCII` and `netsec.ToUnicode` apply the same ContextJ validation and
preserve joiners that meet the context requirement.
