# 请求级模拟作用域与异步身份审计

本模块是 `example.org/netsec/q0113`，提供一个可导入的 Go 服务宿主模拟作用域
（`netsec.Host` / `netsec.Thread` / `netsec.Scope`），以及一个针对遗留实现的
确定性攻击轨迹搜索器（`audit` 子包）。

## 核心约束

- 进程主令牌固定在 `Host` 中；线程栈的隐式栈底就是主令牌。
- 请求令牌只能由 `Authenticator` 签发，绑定连接 ID、请求 ID、连接代次、
  级别和宿主单调时钟截止时刻。截止时刻没有延长接口。
- `Thread.Bind`/`Enter` 把线程限制在当前请求；`Enter` 会重新验证令牌，
  拒绝零值、主令牌、过期令牌、已结束请求和另一活跃请求的令牌。
- `Identification` 只能读取身份快照；`Impersonation` 只能访问本机资源；
  `Delegation` 才能访问远端资源。检查在每次资源调用前执行。
- `SpawnAsync` 必须显式传 `NoIdentity` 或 `Capture`。`PolicyUnspecified`
  直接报错，不能用零值静默继承身份。
- `Capture` 复制只读 `Snapshot`，默认级别降为 `Identification`。保留
  `Impersonation`/`Delegation` 需要 `Host.Authorize` 签发的不可伪造
  `Grant`，且任务截止时刻、授权截止时刻都不晚于请求截止时刻。
- 任务句柄绑定请求代次。请求结束（`Request.End`）、连接重用
  （`Connection.Reuse`）按 `(连接, 代次)` 撤销该代全部任务。
- 回调开始时 `Task.Run` 重验句柄；`TaskContext.Check/Access` 在每次本机或
  远端调用前再次重验。回调返回后，逃逸出去的 `TaskContext` 也无法再访问。
- `RunScoped`/`RunScopedContext` 在正常返回、panic、取消时恢复完整旧栈，
  包括外层模拟帧。`RevertToSelf` 在嵌套栈上直接拒绝，而不是清空外层。

## 基本用法

```go
clock := netsec.NewManualClock(netsec.Instant(100))
host := netsec.NewHost(netsec.Identity{Name: "orders"}, netsec.WithClock(clock))
auth := host.Authenticator()

conn, _ := auth.OpenConnection("conn-1")
req, _ := auth.BeginRequest(conn, "req-1", netsec.Impersonation,
    clock.Now().Add(5*time.Second))

scope, _ := host.NewScope(req, host.NewThread())
err := scope.RunScoped(req.Token(), func() error {
    return scope.Access(netsec.LocalResource, func() error {
        // perform the local call while the thread token is live
        return nil
    })
})
if err != nil {
    return err
}

// Async work must state its policy explicitly.
task, _ := scope.SpawnAsync(netsec.Capture, func(ctx context.Context, tc *netsec.TaskContext) error {
    // Default Capture snapshot is Identification: identity reads succeed,
    // resource calls return ErrIdentityCannotAccessResources.
    _, err := tc.ReadIdentity()
    if err != nil {
        return err
    }
    return nil
})
err = task.Run(context.Background())
```

保留更高级别需要宿主授权：

```go
grant, _ := host.Authorize(req, netsec.Delegation, req.Deadline())
task, _ := scope.SpawnAsync(netsec.Capture, func(ctx context.Context, tc *netsec.TaskContext) error {
    return tc.Remote(func() error { return nil })
}, netsec.CaptureAtLevel(netsec.Delegation, grant))
```

`Scope.SpawnAsync` 调用的是线程严格的 `Host.SpawnAsyncOn`，只会捕获当前线程
中属于该请求的令牌，不会借用池中另一请求的令牌。

## 待审遗留实现

`audit` 子包包含一个故意脆弱的遗留模型 `audit/legacy.go`：

- Capture 不降级，保留原 Delegation 级别；
- 请求结束和连接代次重用不撤销捕获的身份；
- 回调直接使用池线程当前令牌，任务句柄从不重验；
- 线程栈上的旧帧不会随请求结束清除。

`ShortestViolation` 用 BFS 枚举两个请求、一个池线程上的所有动作交错，
返回最短的“回调成功使用已结束/过期身份或另一请求身份访问资源”轨迹。

## 审计结果

```text
$ go run ./cmd/audit

shortest violation (6 steps):
01  auth(R1)           authenticated R1 on C1 generation 1 (Delegation)
02  enter(R1)          pushed R1/1@C1(gen=1,Delegation); depth=1
03  spawn-capture(R1)  Capture copied thread token ... without downgrade or generation check
04  end(R1)            ended R1 generation 1; stale thread frames and tasks were not revoked
05  start-callback(R1) callback R1 installed captured R1/1@C1(gen=1,Delegation) ...
06  access-local(R1)   callback R1 performed local access as R1/1@C1(gen=1,Delegation) ...
violation: callback R1 used ended identity R1 generation 1

shortest cross-request violation (8 steps):
... auth(R1) ... end(R1) ... auth(R2) ... start-callback(R1) ... enter(R2) ...
... access-local(R1) ...
violation: callback R1 used another request's identity R2 generation 2

coverage violation (10 steps):
... nest(R1) ... exit(R1) ... end(R1) ... auth(R2) ... enter(R2) ...
... start-callback(R1) ... access-remote(R1) ...
violation: callback R1 used ended identity R1 generation 1 (connection now generation 2)
```

覆盖轨迹同时包含嵌套模拟、外层恢复、连接/线程池重用、请求结束后的回调
以及远端 `Delegation` 访问。`CrossRequestViolation` 单独搜索“另一活跃请求
身份”变体。

## 验证

```text
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/audit
```

测试覆盖访问级别矩阵、伪造/主令牌、精确 `Exit`、panic 与取消后的外层恢复、
连接代次撤销、显式 Capture/NoIdentity、授权和截止约束、句柄逐次重验、
线程池重用及最短轨迹搜索。
