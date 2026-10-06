# prof — 嵌套调用的包含/独占时间分析库

输入开始/结束事件流，按线程严格栈配对成调用森林，分别计算包含时间
（inclusive）与独占时间（exclusive），并用时间片占用模型（sweep-line）
独立核对结果。

## 事件模型

```go
Event{Type: EventStart|EventEnd, Thread: "worker-1", Func: "f", Time: 42}
```

- 同一线程内事件必须严格 LIFO 配对；异步子任务在自己的线程上发事件，
  因此是调用森林中独立的根，不会扣减父任务的独占时间。
- 同一线程的事件按时间戳稳定排序，同刻事件保持输入顺序，零时长调用
  可以确定性配对。

## 时间口径

- `Call.Inclusive()` = End − Start；`Call.Self` = Inclusive − 同线程直接子调用之和。
- `FuncStats.Inclusive/Exclusive` 是所有激活（跨线程）的朴素求和——递归
  或异步重叠时会超过真实耗时。
- 时间片占用模型对所有调用区间做 sweep-line 积分：
  - `Report.WallBusy`：所有线程占用区间的并集（真实忙碌时长）；
  - `Report.ThreadBusy`：单线程忙碌时长（线程内无重叠）；
  - `FuncStats.WallUnion`：函数处于活跃状态的区间并集。
- 重叠异步耗时不能直接相加：`TotalExclusive() ≥ WallBusy`，差值
  `Report.Overlap()` 即跨线程重叠量；递归时 `WallUnion ≤ Inclusive`。

## 坏栈诊断

`Report.Diagnostics` 记录三类可恢复问题，恢复后仍满足全部不变式：

- `DiagOrphanEnd`：栈空时收到 End，忽略；
- `DiagMismatchEnd`：End 不匹配任何未关闭调用，忽略；
- `DiagUnclosedStart`：Start 未配对（被失配 End 打断或线程结束），
  在当前时间戳强制关闭。

## 核对

`Verify(r)` 用占用模型核对树形计算的不变式，返回违规列表（空即一致）：

1. 独占时间总和 == 各线程忙碌时间之和；
2. `WallBusy ≤ TotalExclusive`；
3. 每个函数 `WallUnion ≤ Inclusive`；
4. 每个调用 `0 ≤ Self ≤ Inclusive`。

## 测试

```sh
go test ./...
```

覆盖：嵌套包含/独占、递归、同刻事件、坏栈诊断与恢复、异步重叠不可
相加、异步子任务不消耗父任务独占时间。
