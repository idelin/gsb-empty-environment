# OIDC登录事务混淆防护

一个站点对接多个身份提供者，回调错配可能把甲方响应用于乙方登录。给Go登录服务实现事务包，开始时绑定浏览器会话、issuer、nonce、state、PKCE摘要及重定向URI；完成时使用该事务固定的验证器和换码器，一次性消费在全部验证成功后发生。用本地伪提供方测试交叉回调、同时登录两个账号、换码超时和无效响应不烧毁正确事务。

## 设计

- `Manager.Begin` 创建事务并钉住全部安全参数：浏览器会话、issuer、state、nonce、PKCE S256 摘要、重定向 URI，以及该 issuer 专属的 `Exchanger`/`Validator` 实例（`transaction.go`）。
- `Manager.Complete` 只使用事务上固定的协作者，绝不从回调输入推导端点或验证器；先做零网络绑定校验（state/会话/issuer/PKCE/redirect URI），再换码、验签、核 issuer 与 nonce。
- 一次性消费发生在全部验证成功之后：任何失败（含换码超时、伪造回调）都保留事务，合法回调可重试；`TryLock` + `consumed` 标志保证并发回调只换码一次。
- `HTTPExchanger`（`http_exchanger.go`）绑定固定 token 端点；`HMACValidator`（`hmac_validator.go`）验证签名、issuer、exp/iat 与 nonce。
- `FakeProvider`（`fake_provider.go`）是经 `http.RoundTripper` 拦截的内存伪 IdP，无需端口，支持注入延迟、错误状态、畸形 JSON、错误签名/issuer。

## 测试

`go test -race ./...` 覆盖：交叉回调混淆（三种变体）、两账号并发登录、换码超时后重试成功、五类无效响应不烧毁事务、并发完成单次消费、TTL 过期与清理、Begin 入参校验。
