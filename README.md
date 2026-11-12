# Memcached租户前缀代理

多租户 Memcached 代理的请求规范化包。它在转发前给文本协议中的键加租户前缀，严格校验存储块边界，并把上游应答限制在当前租户内。

## 安全不变量

- 加前缀后的键不超过 250 字节，且原键与前缀都不含空白、ASCII 控制字符或 DEL。
- 连接首字节选择 `text` 或 `binary`（`0x80`），此后不能切换；文本连接收到 `0x80`、二进制连接收到文本都返回 `ErrProtocolSwitch`。
- 文本请求必须使用 CRLF。`set/add/replace/append/prepend/cas` 的数据块必须恰好等于声明长度，后面紧跟 CRLF；`noreply` 走完全相同的校验。
- 文本 `VALUE` 应答按声明的数据长度精确读取并校验尾部 CRLF；跨租户条目连同其数据块被完整消费后丢弃，同租户键去掉前缀再下发。二进制应答按同样规则处理键。
- 二进制协议默认关闭，连接层只接受探测并锁定协议，不把 `0x80` 包当作文本命令；需要二进制上游时显式设置 `Config.AllowBinary`。`NormalizeBinary` 是显式二进制入口，不受该开关影响。

租户前缀由部署方分配；建议以分隔符结尾（例如 `tenant-42:`），并保证任何前缀都不是另一个租户前缀的前缀，否则 `t1` 与 `t10` 这类命名会在应答过滤时产生歧义。

## 连接处理

```go
norm, err := netsec.NewNormalizer("t42:", &netsec.Config{
    MaxValueLength: 1 << 20,
})
if err != nil {
    // invalid prefix or limits
}

session := norm.NewSession(client)
upstreamReader := bufio.NewReader(upstream)

for {
    req, err := session.ReadRequest()
    if err != nil {
        // EOF ends the connection; every other error is fatal for this connection.
        return
    }
    if _, err := upstream.Write(req.Bytes); err != nil {
        return
    }
    if req.ResponseKind == netsec.ResponseNone {
        if req.CloseAfter {
            return
        }
        continue
    }
    resp, err := norm.ReadResponse(upstreamReader, req)
    if err != nil {
        return
    }
    if _, err := client.Write(resp); err != nil {
        return
    }
    if req.CloseAfter {
        return
    }
}
```

`Normalize`、`NormalizeText`、`NormalizeBinary` 用于已完整读出一个请求的场景；它们拒绝请求之后的剩余字节。连接循环应使用 `NewSession`，因为它保留协议锁并允许流水线请求。

## 验证

```sh
go test ./... -race
```

测试覆盖超长键、空格/控制字符、数据块多一字节/少一字节、`noreply`、跨租户 multiget、二进制探测和双向中途切协议。
