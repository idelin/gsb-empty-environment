# 代理口令跨来源擦除

转发客户端在重定向后可能把代理凭据当成源站凭据发送。为Go传输宿主实现认证首部分区组件，源站与代理凭据分别绑定受信身份和挑战范围，重定向只重新评估源站凭据，换代理必须新挑战；连接池复用不能扩大范围。用两个本地代理和两个源站测试实际收到的首部及失败重试。

## 设计目标

针对转发客户端（forward proxy client）在重定向与代理切换场景下的认证首部
越权问题，本包在 `net/http` 的 `RoundTripper` 之外包一层认证分区宿主
`AuthTransport`，强制以下不变量：

1. **角色分区**：源站（`Authorization` / 401）与代理
   （`Proxy-Authorization` / 407）各持一个互不相通的凭据保险库，
   凭据提供方接口也按角色拆分，编译期即无法互相顶替。
2. **身份绑定**：凭据按规范化身份（scheme + host:port）登记，未登记的
   身份一律不发送任何首部（`ErrUntrustedIdentity`）；可选 realm 白名单
   进一步把凭据限制在受信挑战域内。
3. **挑战范围**：会话按 `(身份, scope)` 缓存。Basic 的 scope 含 realm，
   Digest 的 scope 含 realm + nonce；服务器 `stale=true` 轮换 nonce 时
   精确作废旧范围并重新应答，绝不跨范围复用。
4. **重定向只重评估源站**：每次 `RoundTrip` 都先无条件剥离两个认证首部，
   再依据当前请求的源站身份与代理身份分别决策。重定向跳转到新主机时，
   源站凭据按新身份重新评估，代理凭据对整条链保持其自身身份绑定。
5. **换代理必须新挑战**：代理身份来自 `ProxyFunc`，切换代理即身份变化，
   旧代理会话不会命中；底层 `http.Transport.Proxy` 被收口并剥离 URL
   userinfo，连接缓存键与代理身份判定一致，杜绝 net/http 隐式认证。
6. **连接池不扩权**：认证状态只存在于保险库，不与任何 TCP 连接绑定。
   连接池仅复用字节流；每个请求的首部完全由身份与保险库重新决定。
7. **有界重试**：单次 `RoundTrip` 内 401/407 重试上限为
   `maxAuthRetries=2`，口令错误不会死循环；不可重放的请求体直接返回
   `ErrNonReplayableBody`。

## 关键类型

- `Identity`：认证分区主键，`OriginIdentity` / `ProxyIdentity` 分别从
  请求 URL 与代理 URL 计算。
- `StaticCredentials`：按受信身份登记的静态凭据表，同时实现
  `OriginCredentials` 与 `ProxyCredentials`（调用时分别注入两个角色）。
- `credentialVault`：单角色保险库，按 `(Identity, scope)` 存放 `Session`。
- `AuthScheme`：认证方案抽象，内置 `basicScheme` 与 `digestScheme`
  （Digest 支持 qop=auth、nc/cnonce 自增与 stale 重挑战）。
- `AuthTransport`：实现 `http.RoundTripper` 的分区宿主，`Client()` 返回
  绑定它的 `http.Client`。

## 测试策略

沙箱禁止回环 TCP/Unix 监听，因此测试用 `net.Pipe` 构建内存网络
（`testhelpers_test.go` 中的 `memoryNetwork`），两端仍是真实的
`http.Server` 与 `http.Transport`，HTTP 语义、首部收发、连接池、重定向
全部走标准库真实代码路径。

拓扑为 **两个源站 + 两个代理**，每个对端都用 `recorder` 记录实际收到的
`Authorization` 与 `Proxy-Authorization` 首部：

- 源站 A/B：Basic 挑战，A 的 `/redirect` 认证后 302 到 B；另有独立
  Digest 源站支持 `stale=true` nonce 轮换。
- 代理 1/2：独立 realm 与口令的转发代理，无 `Proxy-Authorization` 即
  407，转发前剥离逐跳代理首部。

覆盖用例（`auth_partition_test.go`，共 12 个）：

- Basic 401 握手、重试与缓存复用
- 跨源站重定向只重评估源站凭据、不受信目标不发首部
- 代理 407 与源站 401 的角色隔离、代理口令不到达源站
- 代理 + 重定向组合：代理凭据跨跳保持、源站凭据逐跳重评估
- 切换代理触发全新 407、切回后按身份复用
- 连接池复用不扩权、并发请求分区、调用方自带首部被强制剥离
- 口令错误有界重试、无提供方时挑战透传、Digest stale 重挑战、
  realm 白名单越界拒绝

## 运行

```
go test -race ./...
```
