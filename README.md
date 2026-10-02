# 断点续传位点管理与崩溃安全恢复器

消费端多线程并行处理变更事件时，追踪"已被所有线程完整消费的最小连续位点"
（Low Watermark，低水位），并将其崩溃安全地持久化；重启后从该位点平滑续跑，
保证不漏掉任何未完成事件（at-least-once，水位之后的事件可能重复投递）。

## 结构

- `checkpoint/manager.go` — 核心组件：
  - `Manager`：并发安全的位点追踪器。`Ack(pos)` 报告消费完成，乱序 Ack 时
    水位仅在连续处推进；水位每次推进都原子落盘（临时文件 + fsync +
    rename + 目录 fsync），崩溃任一时刻文件要么是新水位要么是旧水位。
  - `Open(path, start)`：打开/恢复管理器；持久化文件存在则从中恢复水位。
  - `LowWatermark()`：当前安全重放起始点。
  - `ReadCheckpoint(path, fallback)`：独立进度读取器，供重启后的进程直接
    读取安全重放起始点。
- `checkpoint/manager_test.go` — `go test` 测试，含验收场景。
- `cmd/demo/main.go` — 可运行样例。

## 运行测试

```sh
go test ./checkpoint/ -v
```

## 运行样例

```sh
go run ./cmd/demo
```

## 验收场景

10 个工作线程并行消费位点 1000..2000；线程 3 处理较慢卡在 1050；
系统崩溃重启后，进度读取器准确返回 1050 作为安全重放起始点。
见 `TestAcceptance_CrashResumeAtStuckPosition`。
