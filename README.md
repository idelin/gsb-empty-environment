# 设备授权码组件

`netsec` 是供 Go 授权服务器导入的 RFC 8628 设备授权流程实现。组件只负责设备码、用户码、轮询限流、用户批准和一次性兑换；HTTP 路由、客户端认证和业务令牌策略由调用方提供。

## 安全属性

- 设备码为 32 字节密码学随机数（base64url，256 位）；用户码为 10 字节随机数（Crockford base32，80 位），两者独立生成、独立索引。
- 内存中只保存设备码和用户码的 SHA-256 域分离哈希，不保存明文代码。
- 未批准时按 `PollInterval` 拒绝过快轮询；批准前累计轮询超过 `MaxPolls` 后设备码永久失效，之后的用户批准也无法恢复兑换。
- 用户码验证失败使用固定窗口限流；超过 `UserCodeFailureLimit` 后，窗口结束前拒绝继续尝试。
- 兑换同时校验 `clientID`、设备码和服务器端批准会话；批准会话记录用户 `Subject`、会话 ID、scope、claims 和有效期。
- 已批准设备码使用 `stateRedeeming` 原子预占，保证并发兑换最多只有一个成功令牌。
- 设备码超时、批准会话超时、跨 client 兑换、重复兑换和用户拒绝均不会签发令牌。

## 使用方式

```go
manager, err := netsec.New(netsec.Config{
    DeviceCodeTTL:         10 * time.Minute,
    PollInterval:          5 * time.Second,
    MaxPolls:              30,
    ApprovalTTL:           5 * time.Minute,
    UserCodeFailureLimit:  10,
    UserCodeFailureWindow: time.Minute,
    VerificationURI:       "https://login.example.com/device",
})
if err != nil {
    return err
}

// 设备授权端点
authorization, err := manager.Authorize(ctx, authenticatedClientID)

// 用户在已认证会话中提交 authorization.UserCode
approval, err := manager.Approve(ctx, userCode, netsec.ApprovalSession{
    ID:              authenticatedSessionID,
    Subject:         userID,
    ClientID:        authenticatedClientID, // 可选；非空时必须匹配
    AuthenticatedAt: loginTime,
    ExpiresAt:       sessionExpiry,         // 可选；会受到 ApprovalTTL 和设备码有效期约束
    Scope:           []string{"openid", "profile"},
})

// 设备轮询/兑换端点
token, err := manager.Redeem(ctx, authenticatedClientID, deviceCode)
```

`Config.ValidateApprovalSession` 可接入授权服务器自己的登录会话校验；`Config.IssueToken` 可用 JWT 或其他令牌实现替换默认的 32 字节不透明随机令牌。回调失败时兑换状态会回退，设备可以重试。

## 错误映射

`netsec.CodeOf(err)` 返回适合令牌端点响应的 OAuth 错误码：

| 场景 | 错误 | 错误码 |
| --- | --- | --- |
| 等待用户批准 | `ErrAuthorizationPending` | `authorization_pending` |
| 轮询过快 | `ErrSlowDown` | `slow_down` |
| 超过轮询次数 | `ErrPollLimitExceeded` | `expired_token` |
| 设备码或批准超时 | `ErrExpiredToken` | `expired_token` |
| 用户拒绝 | `ErrAccessDenied` | `access_denied` |
| 跨 client、未知设备码、已消费 | `ErrClientMismatch` / `ErrInvalidDeviceCode` / `ErrAlreadyUsed` | `invalid_grant` |
| 用户码错误或猜测过多 | `ErrInvalidUserCode` / `ErrTooManyAttempts` | `invalid_grant` / `slow_down` |

对外响应时建议只返回 OAuth 错误码，不要把 `ErrClientMismatch` 等内部描述暴露给客户端。

## 默认值

| 配置 | 默认值 |
| --- | --- |
| `DeviceCodeTTL` | 10 分钟 |
| `PollInterval` | 5 秒 |
| `MaxPolls` | 30 |
| `ApprovalTTL` | 与 `DeviceCodeTTL` 相同 |
| `UserCodeFailureLimit` | 10 |
| `UserCodeFailureWindow` | 1 分钟 |
| `TokenTTL` | 1 小时 |

运行验证：

```sh
go test ./...
go test -race ./...
go vet ./...
```
