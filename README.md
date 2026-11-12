# DNS Cookie 时间戳与源地址门禁

攻击者伪造受害者源地址发 UDP 查询，借大应答放大。为Go DNS服务写可导入的 Cookie 门禁：客户端 Cookie 须8字节；服务端 Cookie 16字节：版本1、3字节保留、4字节时间戳、8字节 HMAC-SHA256(密钥, 客户端Cookie‖前8字节‖客户端IP) 截断；时间戳早不超1小时、晚不超5分钟，按32位序列号回绕。缺服务端 Cookie 只回不大于请求的 TC 应答，无效回 BADCOOKIE。测伪造 IP、过期、回绕。

## 用法

```go
gate, err := netsec.NewCookieGate(secret) // secret 应为长期随机密钥
if err != nil {
	// secret 为空
}

decision, challenge, err := gate.Check(query, clientAddr.IP)
switch decision.Status {
case netsec.CookieValid:
	// 校验通过：正常解析并生成应答
case netsec.CookieMissingServer:
	// challenge 是 TC 应答，且长度不超过 query
case netsec.CookieInvalid:
	// challenge 是 extended RCODE 23 (BADCOOKIE)，尽量附带新 Server Cookie
}
```

`Check` 只处理 DNS wire 数据，不依赖具体 DNS 库。服务端生成正常应答时，可使用
`decision.ClientCookie` 和 `gate.GenerateServerCookie(...)` 在应答的 OPT 中回填
Cookie。

## 安全规则

- Client Cookie 必须恰好 8 字节。
- Server Cookie 为 16 字节：`version=1`、3 字节全零保留、4 字节大端 Unix 秒、8 字节截断 HMAC-SHA256。
- HMAC 输入为 `Client Cookie || Server Cookie 前 8 字节 || 客户端 IP`，IPv4 使用 4 字节、IPv6 使用 16 字节。
- 时间戳使用 RFC 1982 的 32 位序列号比较：向前最多 1 小时，向后最多 5 分钟。
- 只有 Client Cookie、没有 Server Cookie 时返回不含附加区的 TC 应答，响应长度不超过请求。
- Cookie 缺失、长度错误、版本/保留位错误、MAC 错误、过期或过未来时返回 BADCOOKIE。

`go test ./...` 覆盖伪造源 IP、过期/未来时间戳、UTC 时间 32 位回绕，以及 TC/BADCOOKIE wire 格式。
