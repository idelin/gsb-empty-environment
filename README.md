# XPC对端审计令牌绑定

iOS 守护进程用 XPC 接收应用请求，若按对端 pid 校验代码签名，攻击者可在检查后 exec 另一个二进制，或在 pid 被复用后借用已通过的检查调用特权方法。为 Go 编写的权限代理写供连接建立和每条消息处理调用的对端校验包：输入连接的审计令牌（pid、pidversion、euid）、代码要求判定结果（团队 ID、签名标识、是否平台二进制）、exec 与退出事件、方法调用所需的授权键；放行结果绑定整个审计令牌而不是 pid，pidversion 变化后须重新判定且旧结果作废；授权键按当前令牌对应的签名身份查询，不用连接建立时缓存的值；团队 ID 与签名标识须同时匹配登记项。用检查后 exec、pid 复用、仅标识相同团队不同和合法调用验证。

## `peerauth` 包

`peerauth/` 提供连接建立与逐消息两条调用路径，核心不变量：

- 判定结果以完整审计令牌 `AuditToken{pid, pidversion, euid}` 为键，绝不以 pid 单独索引；pidversion 或 euid 变化必然缓存未命中并触发重新判定，旧结果无法被借用。
- 签名身份由注入的 `IdentityEvaluator` 在每次判定时实时求值（生产实现封装 `SecCode` 代码要求校验），包内不接受连接建立时缓存的身份。
- `Authorize` 按当前令牌对应的身份查登记表 `Grant`，团队 ID、签名标识、平台二进制标记三者同时匹配；授权键缺失返回 `ErrUnauthorized`，身份未登记返回 `ErrPeerNotRegistered`。
- `NotifyExec`/`NotifyExit` 按 pid 主动清除缓存，作为 pidversion 机制之外的纵深防御；判定失败不缓存。

### 集成点

```go
validator, _ := peerauth.NewValidator(secCodeEvaluator, peerauth.Grant{
    TeamID:    "TEAMGOOD",
    SigningID: "com.example.agent",
    AuthKeys:  []string{"agent.read", "agent.write"},
})

// XPC 新连接回调
_ = validator.ValidateConnection(auditToken)
// 每条消息回调，authKey 为该方法调用所需授权键
err := validator.Authorize(auditToken, "agent.write")

// 进程生命周期事件
validator.NotifyExec(pid)
validator.NotifyExit(pid)
```

### 验证

`go test -race ./peerauth/` 覆盖：检查后 exec（事件清除 + 新 pidversion 重判定 + 旧令牌作废）、pid 复用（新 pidversion/euid 无法借用旧授权）、签名标识相同但团队不同被拒、平台二进制标记联合匹配、合法调用的授权键边界、判定器瞬时失败恢复与并发授权。
