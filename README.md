# .netrc 主机名精确匹配

`netsec` 提供一个可导入的 `http.RoundTripper`，按 `.netrc` 为请求附加 Basic
认证。凭据只发给规范化后完全相等的机器名，避免因重定向、近似域名或
`default` 条目把凭据发送到攻击者控制的主机。

## 使用

```go
transport, err := netsec.NewRoundTripper(
    "/path/to/.netrc",
    http.DefaultTransport,
    false, // 是否允许把凭据发送到明文 HTTP
)
if err != nil {
    log.Fatal(err)
}

client := &http.Client{Transport: transport}
```

也可以先从 `io.Reader`、字节数据或字符串构造 `*Netrc`：

```go
n, err := netsec.ParseNetrcString(netrcData)
if err != nil {
    log.Fatal(err)
}
transport := netsec.NewRoundTripperFromNetrc(n, http.DefaultTransport, false)
```

## 匹配规则

- `machine` 与请求 URL 的主机名先转为小写，IDN 标签转为 punycode，并去掉末尾的点。
- 主机名必须整名相等，不做后缀或子域名匹配；端口不参与比较。
- `default` 只可能用于重定向链中的第一个请求，且仅当其原始主机没有对应的
  `machine` 条目。
- 每个重定向请求都会重新匹配。跳到其他主机时不会使用 `default`；跳回原主机时
  也必须存在对应的 `machine` 条目。
- 调用方已有的 `Authorization` 头不会被覆盖。
- 默认不向明文 HTTP 发送凭据；只有构造时显式启用才会发送。
- `macdef` 定义的脚本内容会被完整跳过，直到下一个空行，不会把其中的
  `machine` 或 `password` 当作配置。
