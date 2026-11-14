# CSRF令牌跨会话使用仍然有效

支付表单的CSRF令牌只按用户而非会话签发，攻击者从自己的会话取得令牌后诱导受害者提交，令牌被接受。请实现供Go支付服务调用的可导入防跨站请求组件。令牌绑定会话标识、用户、动作、表单实例与短有效期，提交时逐项核对且一次性消费；来源或sec-fetch-site与请求类型不符即拒绝。GET不得触发写操作，跨站跳转与表单提交使用不同策略。用跨会话令牌、跨用户、重放、跨站GET写操作、合法表单和并发提交验证令牌只保护其绑定会话。

## 组件说明

模块 `example.org/netsec/q0297`（包名 `netsec`）提供可导入的 CSRF 防护组件：

- `csrf.go` — `TokenManager` 签发 HMAC 签名令牌，绑定会话 ID、用户、动作、一次性表单实例与短有效期；`Validate` 逐项核对并原子消费（`TokenStore` 接口，`MemoryTokenStore` 为默认实现）。
- `middleware.go` — `Protector.ProtectWrite`（表单提交策略：仅非安全方法、Origin/Sec-Fetch-Site 同源校验、令牌校验）与 `Protector.ProtectRead`（跨站跳转策略：顶层导航放行，跨站子资源请求拒绝）。

```go
m, _ := netsec.NewTokenManager(secret, 2*time.Minute, netsec.NewMemoryTokenStore())
p := netsec.NewProtector(m, "https://pay.example.com")
mux.Handle("/transfer", p.ProtectWrite("transfer", transferHandler))
// 渲染 表单时：token, _ := m.Issue(sessionID, userID, "transfer")
```

测试覆盖跨会话令牌、跨用户、重放、跨站 GET 写操作、合法表单、并发提交等场景：`go test -race ./...`。
