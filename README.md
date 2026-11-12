# CAS服务票据单次消费

CAS服务票据可能被同时提交给两个验证请求，或拿去验证另一个服务URL，攻击者借此换取登录身份。为Go单点登录服务实现票据验证包，供serviceValidate处理器调用：ST只能成功消费一次，service须与签发时规范化后相同，十秒过期，renew=true要求票据来自本次凭据认证，代理票据不能冒充服务票据。用并发双验证、改service和过期边界验证。

## 用法

```go
store := netsec.NewTicketStore()

// 第三个参数表示该票据来自一次新的凭据认证（而非 SSO 会话）。
st, err := store.IssueServiceTicket("alice", "https://app.example.com/callback", false)
if err != nil {
    log.Fatal(err)
}

// serviceValidate 处理器会原子地校验并消费票据。
http.Handle(netsec.ServiceValidatePath, netsec.NewServiceValidateHandler(store))
```

也可以直接调用验证函数：

```go
user, err := store.ValidateServiceTicket(st, "https://app.example.com/callback", false)
switch {
case errors.Is(err, netsec.ErrTicketUsed):
    // 重放：票据已被成功消费
case errors.Is(err, netsec.ErrServiceMismatch):
    // 票据被拿去验证另一个 service
case errors.Is(err, netsec.ErrTicketExpired):
    // 已到或超过 10 秒有效期
case errors.Is(err, netsec.ErrRenewRequired):
    // renew=true 但票据并非来自凭据认证
case errors.Is(err, netsec.ErrProxyTicket):
    // PT- 票据试图冒充 ST- 票据
}
```

## 安全语义

- `ST-` 票据的查找、有效期/service/renew 检查和 `used` 状态切换在同一个互斥锁内完成，并发验证只有一个成功。失败的验证不消费票据。
- 签发和验证都会先规范化 service URL：scheme/host 小写、去掉默认端口和 fragment、规范化百分号转义；路径和查询参数顺序及大小写保留。
- 票据从签发时刻起 10 秒有效，验证时刻等于 `issuedAt+10s` 即视为过期。
- `renew=true` 只接受签发时标记为凭据认证的票据。
- 票据类型保存在服务端记录中。代理票据（`PT-`）不能被 `ValidateServiceTicket` 接受，修改前缀也无法命中合法票据。
- 票据标识由 `crypto/rand` 生成，使用 256 位随机值加 `ST-`/`PT-` 前缀。
