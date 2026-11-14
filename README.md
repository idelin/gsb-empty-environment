# 授权请求对象被另一个客户端兑换

OAuth推送授权请求后返回request_uri，攻击者拿到该地址用自己client兑换，取得他人授权。请实现供Go授权服务器调用的可导入授权请求组件。request_uri绑定client、请求参数摘要、用户会话与短有效期，兑换时必须同client同参数且一次性消费；并发兑换只有一个成功。请求对象内容不可被后续修改，scope与redirect按原请求核对。用跨client兑换、参数篡改、重放、并发和合法兑换验证请求对象只属于原client。

## 用法

```go
store := netsec.NewStore(netsec.WithTTL(60 * time.Second))

// PAR 端点：客户端推送授权请求参数，绑定 client 与用户会话。
requestURI, err := store.Push(clientID, sessionID, params)

// 授权端点：用户代理携带 request_uri 回来时一次性兑换。
// 必须同一 client、同一会话、同一组参数；并发/重复兑换只有一个成功。
obj, err := store.Redeem(clientID, sessionID, requestURI, params)
```

安全属性：

- `request_uri` 为 256 位随机 URN，绑定 client_id、用户会话、参数 SHA-256 摘要与短有效期；
- 兑换时逐条核对 client、会话、参数摘要，scope 与 redirect_uri 按原请求核对；
- 校验与消费在锁内原子完成，一次性消费，重放与并发兑换均失败；
- 推送时深拷贝冻结参数，对象字段不可导出，访问器返回副本，内容不可被后续修改；
- 校验失败不销毁对象，避免泄露的 request_uri 被用于拒绝服务。

运行测试：`go test -race ./...`
