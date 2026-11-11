# 非执行内存回收检查

动态代码运行器把代码页改成可写前，必须确认没有线程仍会跳入旧页。写供Go宿主调用的退役组件，线程在安全点报告指令区间与返回地址集合；只有全部线程的报告属于同一屏障代且不引用目标页才允许降为可写。用停滞线程、报告迟到及新线程加入测试，缺少证明时保持只读可执行。

## 组件

- `Coordinator`（`retirement.go`）：退役屏障与证明状态机，线程安全。
  - `RegisterThread` / `UnregisterThread` 管理参与线程；屏障打开期间注册的
    线程自动加入当前屏障代，必须自己报告后证明才成立。
  - `BeginRetirement` 打开带单调递增代号的屏障，`CancelRetirement` 放弃。
  - `ReportAtSafepoint` 接收线程在安全点采集的指令区间与返回地址集合；
    代号不匹配或无开放屏障时返回 `ErrStaleGeneration` / `ErrNoBarrier`，
    迟到的旧代报告永远不会被当作新证据。
  - `TryRetire` 非阻塞尝试，`WaitRetire` / `Retire` 阻塞等待条件变量唤醒。
    仅当全部在册线程都提交了同一代、且指令区间与返回地址均不与目标页
    相交的报告时，才调用 `PageProtector.MakeWritable` 降为可写；否则页面
    保持只读可执行，屏障保持打开。
- `CodePage`（`codepage_unix.go`）：基于 `mmap`/`mprotect` 的真实保护器，
  页面初始为 RX，证明通过后才切到 RW，从不同时可写可执行。

## 测试

- 停滞线程：超时返回 `DeadlineExceeded` 且不发生权限切换，补报后成功。
- 报告迟到：旧代报告被拒，证明不完整，页面保持 RX。
- 新线程加入：屏障中途注册的线程成为新参与方，引用旧页时阻止降级，
  到达干净安全点后放行。
- 指令区间或返回地址引用目标页：返回 `ErrPageReferenced`，屏障不关闭。

```sh
go test ./... -race -count=1
```
