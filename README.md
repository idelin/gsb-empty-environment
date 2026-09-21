# tasklock — 任务级锁（优先级继承 / 传递 / 超时撤销 / 死锁诊断）

纯 Go 标准库实现的任务级互斥锁。当低优先级持有者阻塞高优先级等待者时，
持有者**临时继承**等待者的有效优先级；继承沿等待链**传递**（A 等 B 的锁、
B 等 C 的锁 ⇒ C 提升到 A 的优先级）；等待者**超时或被取消**时提升沿链**撤销**；
每把锁独立贡献继承，**释放一把锁不会抹掉另一把锁的继承**；等待成环时返回
可诊断的 `DeadlockError`（含环上任务名）。

## 设计

- `Runtime`：协议状态（持有者、等待队列、有效优先级）由一把互斥锁保护，
  任务阻塞等待时绝不持有它。
- `Task`：`base` 为注入的基础优先级（数字越大越重要），`eff` 为有效优先级，
  `eff = max(base, 其持有的每把锁的等待者 eff 的最大值)`。
- `Lock`：记录持有者与等待者队列；授予时选有效优先级最高、先到者优先的等待者。
- 任何等待图变化（入队 / 超时 / 取消 / 释放）都触发 `propagate` 沿
  wait-for 链重算有效优先级，直到不再变化；边只在死锁检查通过后加入，
  因此链无环、递归必终止。
- 注入点：任务函数经 `Runtime.Go(task, fn)` 注入，失败经 `fn` 的返回值传出；
  时间经 `Clock` 接口注入（`RealClock` / 测试用 `ManualClock`）；
  取消经 `Runtime.Cancel(task)` 注入。

## API 一览

```go
rt   := tasklock.NewRuntime(nil)              // nil = RealClock；测试注入 ManualClock
lock := rt.NewLock("db")
low  := rt.NewTask("low", 1)
high := rt.NewTask("high", 10)

low.Acquire(lock, 0)              // 0/负超时 = 无限等待
done := rt.Go(high, func(t *tasklock.Task) error {
    return t.Acquire(lock, 50*time.Millisecond) // 注入任务函数与超时
})
low.Priority()                    // 10 —— 继承了 high 的优先级
rt.Cancel(high)                   // 注入取消：high 的 Acquire 返回 ErrCanceled，提升撤销
low.Release(lock)                 // 释放后 low 回落到 base=1
```

错误：`ErrTimeout`、`ErrCanceled`、`ErrNotOwner`、`ErrReentrant`，以及
`DeadlockError{Cycle}`（如 `tasklock: deadlock cycle: B -> A -> B`，
可用 `errors.As` 提取环上任务）。

## 运行样例

```
$ go run ./cmd/demo
[   0.0s] C holds lock-2, B holds lock-1     A.eff=9 B.eff=5 C.eff=1
[   0.1s] B blocked on lock-2                A.eff=9 B.eff=5 C.eff=5
[   0.2s] A blocked on lock-1 (transitive)   A.eff=9 B.eff=9 C.eff=9
[   0.2s] A canceled: acquire returned tasklock: acquire canceled
[   0.3s] after A cancel (boost revoked)     A.eff=9 B.eff=5 C.eff=5
[   0.3s] C released lock-2, B granted       A.eff=9 B.eff=5 C.eff=1
[   0.4s] B released both locks              A.eff=9 B.eff=5 C.eff=1
[   0.5s] A acquire with 150ms timeout while C holds lock-1: tasklock: acquire timed out
[   0.5s] after A timeout (boost revoked)    A.eff=9 B.eff=5 C.eff=1
[   0.5s] demo done
```

## 测试（含竞态检测）

```
$ go test -race -v -count=1 .
=== RUN   TestBasicMutualExclusion
--- PASS: TestBasicMutualExclusion (0.05s)
=== RUN   TestPriorityInheritanceBoostAndRevert
--- PASS: TestPriorityInheritanceBoostAndRevert (0.00s)
=== RUN   TestTransitiveInheritanceAndCancelFallback
--- PASS: TestTransitiveInheritanceAndCancelFallback (0.01s)
=== RUN   TestTimeoutRevokesBoost
--- PASS: TestTimeoutRevokesBoost (0.00s)
=== RUN   TestReleaseOneLockKeepsOtherLocksInheritance
--- PASS: TestReleaseOneLockKeepsOtherLocksInheritance (0.00s)
=== RUN   TestGrantOrderPrefersHighPriority
--- PASS: TestGrantOrderPrefersHighPriority (0.05s)
=== RUN   TestDeadlockCycleDiagnosable
--- PASS: TestDeadlockCycleDiagnosable (0.00s)
=== RUN   TestReentrantAcquireRejected
--- PASS: TestReentrantAcquireRejected (0.00s)
=== RUN   TestInjectedTaskFunctionAndFailure
--- PASS: TestInjectedTaskFunctionAndFailure (0.00s)
=== RUN   TestStressRace
--- PASS: TestStressRace (0.67s)
PASS
ok  	tasklock	2.020s
```

场景对照：

- `TestTransitiveInheritanceAndCancelFallback`：A 等 B 的锁、B 等 C 的锁 ⇒
  优先级传到 C；A 取消后提升沿链回落。
- `TestReleaseOneLockKeepsOtherLocksInheritance`：持有者握两把锁，释放一把后
  另一把锁贡献的继承保留。
- `TestTimeoutRevokesBoost`：注入 `ManualClock` 驱动超时，验证提升撤销。
- `TestDeadlockCycleDiagnosable`：双向等待成环，返回含 `B -> A -> B` 的诊断。
- `TestStressRace`：16 任务 × 3 锁 × 嵌套获取 + 并发取消，在 `-race` 下验证。

## 文件

- `runtime.go` — 锁、任务、优先级继承协议、死锁检测
- `clock.go` — `Clock` 接口、`RealClock`、测试注入用 `ManualClock`
- `runtime_test.go` — 全部场景测试（`go test`，可配 `-race`）
- `cmd/demo/main.go` — 运行样例
