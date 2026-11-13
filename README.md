# close_range与CLOEXEC描述符泄漏规划

沙箱启动器在 fork 与 exec 之间要关闭多余描述符，漏掉一个就把宿主的套接字或配置文件交给不可信子进程；close_range 的区间、CLOSE_RANGE_CLOEXEC 与 CLOSE_RANGE_UNSHARE 容易用错。为 Go 启动器写供 exec 前调用的描述符清理规划包：输入描述符表（编号、是否带 O_CLOEXEC、是否在白名单、描述符表是否与其他线程共享）和启动器打算发出的 close_range 调用序列，模拟后报告 exec 时仍然留存的非白名单描述符；first 大于 last 时无效，last 取 UINT_MAX 表示到末尾，带 CLOEXEC 标志只置位不关闭，与其他线程共享表而未带 UNSHARE 时关闭会影响这些线程并须报告；再给出清除白名单之外全部描述符所需的最少调用序列。用白名单夹在中间、缺 UNSHARE、仅置位和空洞区间验证。

## 用法

```go
table := netsec.DescriptorTable{
	Descriptors: []netsec.Descriptor{
		{FD: 1},
		{FD: 4, Whitelist: true},
		{FD: 6},
	},
	Shared: true,
}

plan := netsec.MinimalCleanup(table)
// plan == []CloseRangeCall{
//   {First: 1, Last: 1, Flags: CloseRangeUnshare},
//   {First: 6, Last: 6},
// }

report, err := netsec.Simulate(table, plan)
// report.Leaked 为空；report.Warnings 为空，因为首个调用已 UNSHARE。
```

`Simulate` 按调用顺序模拟包含端点的区间。`CloseRangeCLOEXEC` 只设置关闭执行标志，`BeforeExec` 仍保留描述符；`CloseRangeUnshare` 在当前调用生效并持续影响后续调用。共享表上的关闭调用若未 UNSHARE，会逐项记录在 `Warnings`。`MinimalCleanup` 跨越描述符号空洞合并调用，由白名单分段，并在共享表场景把 UNSHARE 放到首个调用上。
