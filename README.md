# 两阶段额度管理（CPU / IO）

Go 标准库实现。任务按阶段依次申请额度；阶段切换时**先释放旧额度，再申请新额度**，
任务任何时刻至多持有一个阶段的额度，杜绝“占有旧额度等待新额度”的死锁。

## 设计要点

- 额度用带缓冲 channel 实现（`chan struct{}`，容量 = 该阶段额度），配合一个
  `sync.Mutex` 保护的 `used` 计数供快照/断言。全部为 Go 标准同步原语。
- `Manager.Run(ctx, phases, fn)`：
  1. `acquire(新阶段)` —— 在 `select` 中同时等待额度 token 与 `ctx.Done()`；
  2. 申请成功后执行注入的任务函数 `fn(phase)`（任务函数、时间、失败均由调用方/测试注入）；
  3. 阶段切换点：**先 `release(旧)`，再 `acquire(新)`**。
- 半转换状态：等待新额度期间不持有任何额度。此时取消直接返回
  `context.Canceled`/`DeadlineExceeded`，`defer` 不会重复释放。
- 容量为 0：底层 channel 为 nil，`select` 中只有 `ctx.Done()` 可触发，
  即永远无法满足，只能取消退出。
- 异常结束：`fn` 返回错误或 panic 时，`defer` 归还已持有额度；panic 在归还后重新抛出。
  之后全部容量恢复到初值、可被重新申请。

## 文件

- `manager.go` —— `Manager` / `Phase` / `Run` 实现
- `manager_test.go` —— `go test` 用例（channel 注入时序，确定性，不依赖 sleep 赌时序）
- `demo/main.go` —— 可运行演示

## 运行

```sh
go test -race -v ./...     # 竞态检测
go run ./demo              # 演示
```

## 测试结果

`go test -race -count=20 ./...` 连续 20 轮全部通过；`-v` 输出：

```
=== RUN   TestSwapPhasesMakesProgress
--- PASS: TestSwapPhasesMakesProgress (0.00s)
=== RUN   TestZeroCapacityCancelable
--- PASS: TestZeroCapacityCancelable (0.00s)
=== RUN   TestCancelHalfTransition
--- PASS: TestCancelHalfTransition (0.00s)
=== RUN   TestFailureAndPanicReturnAllQuotas
=== RUN   TestFailureAndPanicReturnAllQuotas/error
=== RUN   TestFailureAndPanicReturnAllQuotas/panic
=== RUN   TestFailureAndPanicReturnAllQuotas/error_in_first_phase
--- PASS: TestFailureAndPanicReturnAllQuotas (0.01s)
    --- PASS: TestFailureAndPanicReturnAllQuotas/error (0.00s)
    --- PASS: TestFailureAndPanicReturnAllQuotas/panic (0.00s)
    --- PASS: TestFailureAndPanicReturnAllQuotas/error_in_first_phase (0.00s)
=== RUN   TestPhasesRunInOrder
--- PASS: TestPhasesRunInOrder (0.00s)
PASS
ok  	quota
```

用例覆盖：

- `TestSwapPhasesMakesProgress`：A 持 CPU 想换 IO、B 持 IO 想换 CPU，
  经典互等局面；验证互换成功、无死锁，结束后两阶段用量归零。
- `TestZeroCapacityCancelable`：容量 0 阶段先取消立即返回；持 CPU 后再等
  容量 0 的 IO，只能取消退出，且 CPU 额度已归还。
- `TestCancelHalfTransition`：在“旧额度已释放、新额度等待中”取消，验证退出
  且用量正确；另一个持有任务结束后全部归零。
- `TestFailureAndPanicReturnAllQuotas`：返回错误与 panic 两种异常结束后，
  用量归零，并且完整初值容量（CPU 2 / IO 1）可被再次全部申请。
- `TestPhasesRunInOrder`：CPU→IO→CPU 严格按序执行。

## 演示样例（`go run ./demo`）

```
== 场景1：两任务互换阶段（容量各 1） ==
任务A 进入 CPU（CPU used=1, IO used=1）
任务B 进入 IO （CPU used=1, IO used=1）
任务A 进入 IO （CPU used=1, IO used=1）
任务B 进入 CPU（CPU used=1, IO used=1）
任务A 进入 CPU（CPU used=1, IO used=1）
任务B 进入 IO （CPU used=1, IO used=1）
任务A 结束 err=<nil>
任务B 结束 err=<nil>
互换场景完成后：CPU used=0, IO used=0

== 场景2：IO 容量为 0，取消等待 ==
任务  进入 CPU（CPU used=1, IO used=0），随后准备切换
正在等待容量0的 IO（CPU used=0, IO used=0），现在取消……
任务退出 err=context canceled；取消后：CPU used=0, IO used=0

== 场景3：任务异常返回错误，额度回归初值 ==
任务  进入 CPU（CPU used=1, IO used=0）
任务  进入 IO（CPU used=0, IO used=1）
Run 返回 err=注入的 IO 故障
异常结束后：CPU used=0, IO used=0（应全部为 0，等于初值）
```
