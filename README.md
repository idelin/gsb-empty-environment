# desim — 统一离散事件调度器（Go，仅标准库）

链路、定时器与包到达在同一个离散事件队列中统一调度。时间为整数 tick，
全程不发真实网络流量、不用真实时钟。

## 设计

- **统一调度**（`event.go`）：`Scheduler` 用最小堆按
  `(tick, category, seq)` 排序所有事件。同 tick 先按事件类别决胜
  （`CatPacket` 包到达 < `CatLink` 链路发送完成 < `CatTimer` 定时器），
  同类别再按全局单调序号 `seq` 决胜。因此事件处理中新增的同 tick 事件
  必然排在同类别已挂起事件之后——顺序完全确定。
- **代际句柄取消**：每次调度返回 `Handle{Slot, Gen}`。事件触发或被取消
  时槽位代际 `gen` 递增，旧句柄立即失效：已取消的事件绝不会触发，
  过期句柄也无法误取消复用同一槽位的新事件。堆中条目惰性删除。
- **网络模型**（`sim.go`）：`Packet` / `Queue`（字节容量 FIFO，溢出丢弃）/
  `Link`（串行化速率 + 固定传播时延，均为整数 tick）/ `Node`（BFS 静态
  路由，确定性）。`Generator` 用可取消的定时器事件周期发包，随机源是
  状态可序列化的 xorshift64。
- **Checkpoint**（`checkpoint.go`）：gob 快照调度器（含挂起事件、槽位
  代际、序号计数器）、RNG 状态、拓扑、队列与统计。恢复时从占用槽位
  重建堆，轨迹与连续执行逐行一致（demo 与测试均验证）。

## 运行

```sh
go test ./... -v   # 8 个测试
go run ./cmd/demo  # 演示：取消 + checkpoint/restore 一致性
```

## 测试结果

```
--- PASS: TestSameTickOrdering
--- PASS: TestCancelGenerationalHandle
--- PASS: TestSameTickInsertionDuringDispatch
--- PASS: TestCheckpointRestoreConsistency
--- PASS: TestCancelGeneratorStopsTraffic
--- PASS: TestLinkSerializationAndQueue
--- PASS: TestQueueDrop
--- PASS: TestScheduleInPastPanics
PASS
ok  	desim
```

## 运行样例（`go run ./cmd/demo`）

```
cancelled generator 1 at tick 200; checkpoint at tick 500 (11989 bytes)

continuous          : delivered=54 avg-delay=98.8 ticks drops=99 trace-hash=0d651f14682629e7
checkpoint+continue : delivered=54 avg-delay=98.8 ticks drops=99 trace-hash=0d651f14682629e7
restored+continue   : delivered=54 avg-delay=98.8 ticks drops=99 trace-hash=0d651f14682629e7

OK: continuous, checkpointed and restored traces are identical

== trace lines around the cancellation (tick 200) ==
199 cancel gen=1
204 tx-begin pkt=36 link=1 end=209
204 emit pkt=45 gen=0 A->C size=29
204 drop pkt=45 link=0 reason=queue-full
205 deliver pkt=31 node=C delay=62
```
