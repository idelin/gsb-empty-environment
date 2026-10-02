# fiberdbg — 可暂停、可单步的协程调试器

对 Fiber 调度过程进行**控制反转**的协作式协程调试器（纯标准库）。
调度器没有任何后台 goroutine：所有推进（恢复协程、拨动全局时钟）
都由调试器显式触发，便于在自动化测试中逐指令、逐 Tick 复现与排错。

## 能力

- **断点**：`AddBreakpoint(Breakpoint{FiberID, Instr})`，在指定协程的第 N 条指令处停下
- **暂停全局时钟**：`PauseClock()` 后 `Tick()` 成为空操作，睡眠中的协程永不唤醒
- **单协程单步**：`StepFiber(id)` 只执行某一个协程的一条指令
- **挂起原因查看**：`Snapshot()` / `Stuck()` 报告每个协程的状态与原因
  （等待锁 / 等待 Channel / 睡眠）
- **状态转换字典**：`TransitionMap()` 返回 `map[fiberID][]Transition`，
  记录每次迁移的 Tick、前后状态与原因，可精确定位某协程在特定时隙的假死

## 快速开始

```go
sch := fiberdbg.NewScheduler()
config := fiberdbg.NewChannel("config", 0)

sch.Spawn("initializer", func(f *fiberdbg.Fiber) {
    f.Sleep(2)
    // BUG: 缺少 f.Send(config, cfg) —— 资源永远未初始化
})
sch.Spawn("worker", func(f *fiberdbg.Fiber) {
    f.Recv(config) // 假死于此
})

dbg := fiberdbg.NewDebugger(sch)
dbg.Step()     // 逐指令推动
dbg.Tick()     // 逐 Tick 推动时钟
fmt.Println(sch.Stuck()) // 定位假死协程及其挂起原因
```

## 运行

```sh
go test ./...            # 全部测试（含验收场景）
go run ./examples/deadlock   # 运行样例：单步定位假死
```

## 文件

- `scheduler.go` — Fiber、调度循环、全局时钟、状态转换字典
- `sync.go` — Channel 与 Mutex 原语（阻塞时记录挂起原因）
- `debugger.go` — 断点、时钟暂停、单步、Continue
- `scheduler_test.go` — 单元测试与验收场景
- `example_test.go` — 可执行的文档示例
- `examples/deadlock/main.go` — 完整排错演示
