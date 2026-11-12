# XPC对端审计令牌绑定

iOS 守护进程用 XPC 接收应用请求，若按对端 pid 校验代码签名，攻击者可在检查后 exec 另一个二进制，或在 pid 被复用后借用已通过的检查调用特权方法。为 Go 编写的权限代理写供连接建立和每条消息处理调用的对端校验包：输入连接的审计令牌（pid、pidversion、euid）、代码要求判定结果（团队 ID、签名标识、是否平台二进制）、exec 与退出事件、方法调用所需的授权键；放行结果绑定整个审计令牌而不是 pid，pidversion 变化后须重新判定且旧结果作废；授权键按当前令牌对应的签名身份查询，不用连接建立时缓存的值；团队 ID 与签名标识须同时匹配登记项。用检查后 exec、pid 复用、仅标识相同团队不同和合法调用验证。

## peerverify 包

实现位于 `peerverify/`，核心入口：

- `New(evaluator, registrations...)`：注入代码要求判定器 `Evaluator` 和受信登记项。
- `Connect(token)`：连接建立时对完整审计令牌（pid、pidversion、euid）做首次判定。
- `Authorize(token, requiredKeys...)`：每条消息处理时调用，确保当前令牌已通过判定，并按当前签名身份在最新登记表实时核对授权键。
- `OnExec(token)` / `OnExit(token)`：消费 exec 与退出事件，作废同一审计令牌及同一 pid 下的旧结论。

关键不变量：

- 放行结论以完整 `AuditToken` 为键缓存，绝不以 pid 为键；pidversion 或 euid 变化天然产生新键，强制重新判定。
- 登记项以（团队 ID，签名标识）二元组为键，二者必须同时命中，可额外要求平台二进制。
- 授权键不随连接缓存，`Authorize` 每次实时查询登记表，登记项的运行期增删改立即生效。
- 判定失败与未登记身份不写入缓存，允许条件变化后重试。

集成示例：

```go
v, _ := peerverify.New(codeRequirementEvaluator, peerverify.Registration{
    TeamID:      "TEAMID",
    SigningID:   "com.example.agent",
    AllowedKeys: []string{"config.read", "config.write"},
})

// 连接建立
token := auditTokenFromXPC(conn)
if _, err := v.Connect(token); err != nil {
    connCancel(conn)
    return
}

// 每条消息
if err := v.Authorize(token, "config.write"); err != nil {
    replyError(err)
    return
}

// 进程事件回调
eventMonitor.OnExec(v.OnExec)
eventMonitor.OnExit(v.OnExit)
```

验证：

```sh
go test ./... -race -count=1
go vet ./...
```

测试覆盖检查后 exec、pid 复用（含旧令牌重放）、签名标识相同但团队不同、平台二进制要求、合法调用与授权键运行期撤销等场景。
