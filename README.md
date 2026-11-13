# PAR一次性引用

`example.org/netsec/q0098` 提供一个可导入的、并发安全的 Go 状态机，用于保存授权服务签发的 pushed request URI。

## 行为

- `Issue` 将 `request_uri` 绑定到创建它的 `client_id`，并记录绝对到期时间。
- 创建者在到期前首次调用 `Use` 成功，引用立即转为一次性使用的失效状态。
- 创建者再次使用返回 `ErrReferenceUsed`。
- 其他客户端使用返回 `ErrWrongClient`，且不会消耗该引用。
- 到期后使用返回 `ErrReferenceExpired`。
- 重复注册同一个 `request_uri` 返回 `ErrReferenceExists`，不会覆盖原绑定。
- `Inspect` / `Status` 可读取当前状态：`active`、`used` 或 `expired`。

## 用法

```go
machine := netsec.NewStateMachine()

err := machine.Issue(requestURI, clientID, time.Now().Add(90*time.Second))
// 授权端点收到请求时：
err = machine.Use(requestURI, clientID)
```

测试可使用 `netsec.NewStateMachineWithClock(fakeNow)` 注入时钟，以确定性地验证到期行为。

pushed请求返回的request_uri被重复使用或被另一个客户端拿去。该引用只属于创建它的客户端，到期或使用一次后失效；其他客户端使用即拒绝。创建者在有效期内首次使用成功。可导入的Go状态机接授权服务送来的状态。同一引用用两次，并用另一个客户端用一次。
