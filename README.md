# Expect 100-continue鉴权前正文

客户端带 Expect: 100-continue 时，服务端在鉴权通过前就回 100，会让攻击者先用大正文占住后端，或借多个 Expect 头与代理层的理解差异发起走私。为Go反向代理写可导入的 Expect 处理包，供请求处理调用：Expect 的值只认 100-continue（不分大小写），其他值返回 417；HTTP/1.0 请求带 Expect 忽略；后端鉴权结果返回前不得发送 100，鉴权失败时返回最终状态并关闭连接，不读取请求正文；Content-Length 超过调用方上限时直接返回 413，不发 100；同一请求出现多个 Expect 头，或 Transfer-Encoding 与 Content-Length 并存，一律拒绝；客户端未等 100 就发送正文时，这些字节计入正文预算，超限即关闭；向后端转发时 Expect 已被消耗，不得再转发 Expect 头。用鉴权失败不读正文、超大 Content-Length、多个 Expect、HTTP/1.0 和未等 100 就发正文验证。

## 用法

```go
handler := netsec.NewHandler(netsec.Config{
	MaxBodyBytes: 10 << 20,
	Authenticate: func(r *http.Request) (bool, int) {
		// 这里只做鉴权，不要读取 r.Body。
		if !authorized(r) {
			return false, http.StatusUnauthorized
		}
		return true, 0
	},
	Next: reverseProxy,
})
```

也可以使用中间件形式：

```go
middleware := netsec.Middleware(netsec.Config{
	MaxBodyBytes: 10 << 20,
	Authenticate: authenticate,
})
handler := middleware(reverseProxy)
```

- `MaxBodyBytes` 为 0 时拒绝任何非空正文；设为 `netsec.UnlimitedBodyBytes` 可关闭正文预算。
- 未通过鉴权时返回回调给出的 4xx/5xx 状态（缺省为 401），设置 `Connection: close`，且不读取正文。
- 单个非 `100-continue` 值返回 417；重复 `Expect`、`Transfer-Encoding` 与 `Content-Length` 并存返回 400。
- 声明的 `Content-Length` 超限直接返回 413；分块或未知长度正文在读取过程中超过预算时停止读取、返回 413 并关闭连接。
- 鉴权成功后、调用 `Next` 前删除 `Expect`，因此反向代理不会把已消费的 Expect 再转发给后端。
- 依赖标准 `net/http` 服务端在首次读取正文时自动发送 100；鉴权完成前正文被占位器封锁，不会提前触发 100。

## 测试

```sh
go test -race ./...
```
