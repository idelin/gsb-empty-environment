# 隔离试验输出回收

## `capture` 包

`capture` 提供面向不可信试验进程的有界输出回收：

- `capture.New(Config)` 返回 goroutine 安全的 `*Capture`（实现 `io.WriteCloser`）。
  - `MaxLineBytes`：单行保留头部上限，溢出字节继续排空但丢弃，`Line.Truncated` 置位。
  - `MaxTotalBytes`：已保留证据上限，超限按最旧完成行驱逐（保留尾部），`Snapshot.TotalTruncated` 置位。
  - 硬性内存上界为 `MaxTotalBytes + MaxLineBytes`；`Write` 永不阻塞生产者，管道始终被读到 EOF。
- `capture.Run(ctx, cmd, cfg)` 接管 `exec.Cmd` 的 stdout/stderr：
  - 正常退出返回双流 `Snapshot` 与进程错误；
  - `ctx` 取消时先杀进程组（Unix 下 `setpgid` + 负 PID 信号，Windows 杀直接子进程），再等待两条管道关闭排空，最后 `Wait` 回收，已接收证据不丢失，结果中 `Canceled` 置位。

```go
res, err := capture.Run(ctx, cmd, capture.Config{
    MaxTotalBytes: 64 << 10,
    MaxLineBytes:  4 << 10,
})
fmt.Println(res.Stdout.String(), res.Stderr.TotalTruncated)
```

测试通过再执行测试二进制（`GO_WANT_HELPER_PROCESS`）模拟可控输出进程，覆盖无换行巨串、
双流并发、取消回收证据、突发退出关闭竞态（多轮）与并发快照，可用 `go test -race ./...` 运行。

安全测试运行不可信样本时，输出流可无限增长并阻塞进程清理。写供Go测试宿主调用的有界捕获组件，分别限制总字节、单行字节和保留尾部，超限标记截断但继续排空到终止；取消先停止试验，再等待流关闭并保存已接收证据。用可控输出进程测试无换行巨串、双流并发和关闭竞态。
