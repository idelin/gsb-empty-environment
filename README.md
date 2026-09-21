# tasklock

任务级互斥锁，带**优先级继承**（priority inheritance），仅用 Go 标准库同步原语实现。

## 语义

- 每个 `Task` 有固定的基础优先级（数值越大越紧急）；`EffectivePriority()` 返回含继承提升的当前优先级。
- 低优先级持有者阻塞高优先级等待者时，持有者被临时提升到等待者的优先级。
- **多锁传递**：A 等 B 持有的锁、B 等 C 持有的锁时，A 的优先级沿等待链传递给 C。
- **超时/取消撤销**：`Lock(ctx, ...)` 的等待因 `ctx` 取消或超时结束时，捐赠立即撤销，提升沿链回落。
- 释放一把锁只结束该锁上的捐赠，**不影响**持有者其他锁上的继承。
- **死锁诊断**：会形成等待环的加锁请求立即失败，返回 `*DeadlockError`，`Cycle` 字段给出环路径（如 `b -> a -> b`）。
- 解锁时等待者中有效优先级最高者优先获得锁。

## 用法

```go
var s tasklock.Scheduler
res := s.NewMutex("res")
low := s.NewTask("low", 1)
high := s.NewTask("high", 10)

_ = s.Lock(context.Background(), low, res)   // low 持有
go s.Lock(context.Background(), high, res)   // high 阻塞 -> low 被提升到 10
_ = s.Unlock(low, res)                       // high 获得锁，low 回落到 1
```

任务函数、时序与失败均由调用方（测试）注入：库只提供 `Scheduler.Lock/Unlock`
协议与优先级观察接口，不内置任何调度或定时策略。注入示例见
`injected_workload_test.go`。

## 运行

```sh
go test -race -v ./...   # 全部测试（含竞态检测）
go run ./cmd/demo        # 旁白式演示：提升、传递、取消回落、死锁诊断
```

## 实现要点

- 全部协议状态由 `Scheduler` 内一把 `sync.Mutex` 保护；阻塞的等待者挂在每任务一个的缓冲 channel 上。
- 继承关系是"等待者 -> 持有者"的 donor 边；有效优先级按需沿 donor 图递归取最大值。插入等待边前先走 wait-for 链检测环，因此 donor 图恒为 DAG，递归必终止。
- 取消与授权竞争时，在锁内检查 `waitingOn` 判定谁获胜：授权先到则正常持锁，否则撤销捐赠并返回 `ctx.Err()`。
