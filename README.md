# PAR引用单次与参数覆盖

攻击者重放或换用别的客户端推送的 request_uri，或在授权请求里追加参数覆盖已推送内容。为Go授权服务器写可导入的 PAR 组件：推送须通过客户端认证并拒绝请求体自带的 request_uri；返回 urn:ietf:params:oauth:request_uri: 开头的一次性引用，默认60秒过期；授权端点只取与引用绑定的参数，client_id 与引用所属客户端不同则拒绝，引用只用一次，其余查询参数一律忽略。测重放、换客户端、过期、并发使用。

## par 子包

`par`（RFC 9126 Pushed Authorization Requests）提供两个可直接挂载的
`http.Handler` 组件，以及可替换的 `Store` 接口和并发安全的 `MemoryStore`。

安全保证：

- 推送端点先做客户端认证（`Authenticator` 由接入方实现），认证失败返回
  `401 invalid_client`；请求体自带 `request_uri` 一律 `400 invalid_request`。
- 引用为 `urn:ietf:params:oauth:request_uri:` 加 256 位随机串，一次性使用，
  默认 TTL 60 秒（`WithTTL` 可调），响应含 `expires_in`。
- 授权中间件命中 `request_uri` 时先原子消费（`Consume` 加锁，重放/并发只有
  一次成功），再校验 `client_id` 必须与推送客户端一致，最后用引用绑定的参数
  整体替换查询串，攻击者追加的任何参数都被丢弃。
- 客户端凭据（`client_secret`、`client_assertion*` 等）不会被存入授权参数。

接入示例：

```go
import "example.org/netsec/q0373/par"

func authenticate(r *http.Request) (string, error) {
    // 校验 client_secret_post / client_secret_basic / private_key_jwt 等，
    // 返回认证通过的 client_id。
}

manager := par.NewManager(authenticate) // 可加 par.WithTTL / par.WithStore / par.WithClock

mux := http.NewServeMux()
mux.Handle("POST /par", manager.PushHandler())
mux.Handle("/authorize", manager.AuthorizeMiddleware(yourAuthorizeHandler))
```

测试：`go test -race ./par`，覆盖重放、换客户端、过期、并发（不同引用全部成功、
同一引用仅一次成功）、参数覆盖隔离、请求体 `request_uri` 拒绝等场景。
