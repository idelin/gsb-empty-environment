# desim — 确定性离散事件网络仿真器

纯 Go 标准库实现。链路、定时器、包到达在同一个离散事件调度器上运行；
时间为整数 tick，不产生任何真实网络流量。

## 设计

- **统一调度**：所有事件（定时器 `CatTimer`、包到达 `CatArrival`、链路
  `CatLink`）进入同一个优先队列，排序键为全序 `(tick, category, seq)`。
  `seq` 全局单调递增，因此同 tick 事件先按类别、再按序号决胜；
  事件处理中新增的同 tick 事件序号更大，排在已入队的同类事件之后。
- **代际句柄取消**：`ScheduleAt` 返回 `Handle{slot, gen}`。槽位在事件
  被消费后回收并递增代际，旧句柄因代际不匹配而失效——取消后的旧事件
  不会触发，过期句柄也无法误取消复用槽位上的新事件。
- **checkpoint**：`World.Save` / `LoadWorld`（以及调度器级的
  `Marshal` / `Unmarshal`）用 `encoding/gob` 序列化时钟、序号、
  全部待处理事件（含取消槽位）和实体状态。排序键是带唯一 `seq` 的
  全序，恢复后重建堆的弹出序列与原来完全一致，因此恢复轨迹与连续
  执行逐事件相同。

## 模型

`Source`（定时器周期发包）→ `Queue`（有界 FIFO，满则丢弃）→
`Link`（按 `Rate` 字节/tick 串行化传输 + `Delay` tick 传播时延）→
`Sink`（记录送达轨迹）。

## 运行

```sh
go test ./...
go run ./cmd/simdemo
```

## 运行样例

```
checkpoint @tick=40: 998 bytes, pending events=2, queue=3 packet(s)

restored trajectory (packet deliveries):
 tick  packet
   12  #1
   22  #2
   32  #3
   42  #4
   52  #5
   62  #6
   72  #8
   82  #12

delivered=8 dropped=4 (queue cap=4)
restored trajectory identical to continuous run: true
```

## 测试结果

```
=== RUN   TestCheckpointRestoreMatchesContinuousRun
--- PASS: TestCheckpointRestoreMatchesContinuousRun (0.01s)
=== RUN   TestCheckpointPreservesCancellation
--- PASS: TestCheckpointPreservesCancellation (0.00s)
=== RUN   TestCheckpointSeqContinuity
--- PASS: TestCheckpointSeqContinuity (0.00s)
=== RUN   TestFIFOOrderAndDrops
--- PASS: TestFIFOOrderAndDrops (0.00s)
=== RUN   TestNoDropsWhenLinkKeepsUp
--- PASS: TestNoDropsWhenLinkKeepsUp (0.00s)
=== RUN   TestSameTickCategoryAndSeqOrder
--- PASS: TestSameTickCategoryAndSeqOrder (0.00s)
=== RUN   TestSameTickInsertionFromHandler
--- PASS: TestSameTickInsertionFromHandler (0.00s)
=== RUN   TestCancelPreventsFiring
--- PASS: TestCancelPreventsFiring (0.00s)
=== RUN   TestStaleHandleCannotCancelRecycledSlot
--- PASS: TestStaleHandleCannotCancelRecycledSlot (0.00s)
=== RUN   TestCancelZeroHandle
--- PASS: TestCancelZeroHandle (0.00s)
=== RUN   TestRunUntilBoundary
--- PASS: TestRunUntilBoundary (0.00s)
PASS
ok  	example.com/desim
```

## 文件

- `event.go` — 事件类别、事件、代际句柄
- `scheduler.go` — 调度器：统一队列、取消、步进/运行
- `heap.go` — `(tick, category, seq)` 优先队列
- `checkpoint.go` — 调度器状态 gob 序列化
- `network.go` — Source/Queue/Link/Sink 模型与 World checkpoint
- `cmd/simdemo/main.go` — checkpoint/恢复 演示
- `*_test.go` — 同 tick 顺序、取消、checkpoint 一致性等测试
