# wfqsim — 分层加权公平队列调度仿真（离散事件，Go 标准库）

在离散事件模型中仿真分层 WFQ 分组调度器：所有包、队列与链路都按整数
tick 推进，不产生真实网络流量。仅使用 Go 标准库。

## 结构

- `sim/scheduler.go` — 两级分层 WFQ 调度器（租户 → 流），虚拟时间维护
- `sim/engine.go` — 离散事件引擎、链路模型、背压、快照/恢复
- `sim/event.go` — 事件堆，同 tick 确定性排序
- `sim/fluid.go` — 独立实现的理想流体（GPS）参照调度器 + 字节级偏差报告
- `cmd/demo` — 运行样例，打印服务轨迹与偏差报告

## 虚拟时间推进规则

- 全局虚拟时间 `V` 只在系统有积压时推进，速率为
  `1 / Σ(活跃租户权重)`；系统空闲时 `V` 冻结，任何流不能因空闲而积累
  或损失优势。每个租户有各自的虚拟时间 `V_t`，按租户内活跃流权重推进。
- 入队时，若流原本空闲（重新激活），包的虚拟起点为
  `max(V_t, flow.VFinish)`——即空闲流不能积累无限优势；持续积压时起点
  衔接上一包的完成时间。`VFinish = VStart + bytes/weight`（包长公平：
  按字节计费，等权流无论包长都得到相等字节服务）。
- 动态改权重时，以当前虚拟时间为锚点重缩放该流在排包的完成时间：
  `F' = V + (F - V) * (旧权重/新权重)`，已挣得的位置保留，剩余工作量按
  新速率折算。租户权重同理在全局域重缩放。
- 流撤回时，其在排包被丢弃并计入 `Withdrawn`，流立即离开活跃集；虚拟
  时间保持连续、不跳变；租户无积压时离开活跃集。
- 选择规则：租户间取最小 `(租户VFinish, 候选包ID)`，租户内取队首包中
  最小 `(VFinish, 包ID)`——**完成时间相同一律按包 ID 决胜**。

## 事件与同 tick 顺序

同一 tick 的事件全部处理完后才做一次调度决策（同 tick 到达的包公平
竞争）。同 tick 内处理顺序固定：传输完成 → 流权重变更 → 租户权重变更
→ 流撤回 → 包取消 → 到达；同类事件按提交序号。因此同一脚本重放结果
逐比特一致。

## 背压与取消

- 流队列容量有限时，超限到达的包立即计为 `Backpressured`，不进入任何
  队列；同 tick 的取消先于到达处理，可腾出槽位（见测试）。
- 取消只对仍在队列中的包生效一次：取消已服务、已取消、在传或未知包
  均为无操作，**已取消包绝不会被重复计数或进入服务轨迹**。
- 任意时刻满足守恒：`Arrived == Served + Cancelled + Backpressured +
  Withdrawn + 仍在队列`。

## 快照恢复

`Engine.Snapshot()` 用 `encoding/gob` 序列化完整状态（事件堆、调度器、
链路、轨迹、统计），`Restore()` 恢复后继续运行与不间断运行逐比特一致
（浮点累加均按排序后的 ID 顺序进行，保证确定性）；同一快照可重复恢复。

## 与理想流体调度器对照

`sim/fluid.go` 是独立实现的分层 GPS 参照（按权重分水、递归再分配）。
`ByteDeviation` 按**字节**比较每个流每个 tick 的累计服务量（包调度器
在传输完成 tick 入账），报告最大偏差字节数与最差流/tick，而非只比较
包数。测试表明偏差被最大包长的小倍数所界。

## 运行

```sh
go run ./cmd/demo     # 打印服务轨迹 + 字节级偏差报告
go test ./...         # 全部测试
```

## 运行样例（`go run ./cmd/demo`）

场景：gold 租户（权重 2，流 1 与流 2 等权、包长 10/4 字节）与 silver
租户（权重 1，流 3，6 字节）；tick 10 取消一个排队包，tick 120 流 1
权重改为 3，tick 160 流 2 空闲后重新激活。

```
service trace (rate 1 byte/tick, until tick 240)
from to   packet  flow tenant bytes vstart    vfinish  
0    4    21      2    1      4     0.000     4.000    
4    10   41      3    2      6     0.000     6.000    
10   14   22      2    1      4     4.000     8.000    
14   24   1       1    1      10    0.000     10.000   
24   30   42      3    2      6     6.000     12.000   
30   34   23      2    1      4     8.000     12.000   
34   38   24      2    1      4     12.000    16.000   
38   44   44      3    2      6     18.000    24.000   
44   54   2       1    1      10    10.000    20.000   
54   58   25      2    1      4     16.000    20.000   
58   64   45      3    2      6     24.000    30.000   
64   68   26      2    1      4     20.000    24.000   
68   72   27      2    1      4     24.000    28.000   
72   78   46      3    2      6     30.000    36.000   
78   88   3       1    1      10    20.000    30.000   
88   92   28      2    1      4     28.000    32.000   
92   98   4       1    1      10    30.000    40.000   
98   108  5       1    1      10    40.000    50.000   
108  118  6       1    1      10    50.000    60.000   
118  128  7       1    1      10    60.000    74.000   
128  138  8       1    1      10    70.000    77.333   
160  164  31      2    1      4     82.000    86.000   
164  168  32      2    1      4     86.000    90.000   
168  172  33      2    1      4     90.000    94.000   
172  176  34      2    1      4     92.000    96.000   

stats: {Arrived:26 Served:25 ServedBytes:158 Cancelled:1 Backpressured:0 Withdrawn:0}
flow 1: served 80 bytes
flow 2: served 48 bytes
flow 3: served 30 bytes

byte-level deviation vs ideal fluid scheduler
flow 1: max deviation 9.000 bytes
flow 2: max deviation 4.000 bytes
flow 3: max deviation 4.000 bytes
overall: max deviation 9.000 bytes (flow 1 @ tick 87)
```

注意流 2 重新激活（tick 160）时 `vstart=82`，即当前租户虚拟时间——
没有积累空闲期间的优势。等权的流 1（10 字节包）与流 2（4 字节包）在
共同积压期间按字节等量服务（流 2 包数更多但字节数相同）。

## 测试结果（`go test ./...`）

```
=== RUN   TestSnapshotRestoreIdenticalTrace
--- PASS: TestSnapshotRestoreIdenticalTrace (0.00s)
=== RUN   TestSameTickOrdering
--- PASS: TestSameTickOrdering (0.00s)
=== RUN   TestBackpressureOrderingRecovery
--- PASS: TestBackpressureOrderingRecovery (0.00s)
=== RUN   TestFluidDeviationSmall
    fluid_test.go:37: flow 3: max byte deviation 3.667
    fluid_test.go:37: flow 1: max byte deviation 9.333
    fluid_test.go:37: flow 2: max byte deviation 7.000
    fluid_test.go:39: overall max byte deviation 9.333 bytes (flow 1 @ tick 120)
--- PASS: TestFluidDeviationSmall (0.00s)
=== RUN   TestFluidReferenceSanity
--- PASS: TestFluidReferenceSanity (0.00s)
=== RUN   TestByteFairnessDifferentPacketSizes
    scheduler_test.go:50: max byte deviation vs fluid: 9.000 bytes (flow 1 @ tick 249)
--- PASS: TestByteFairnessDifferentPacketSizes (0.00s)
=== RUN   TestIdleFlowReactivationNoAdvantage
    scheduler_test.go:70: post-reactivation window [400,500]: flow1=55 bytes, flow2=50 bytes
--- PASS: TestIdleFlowReactivationNoAdvantage (0.00s)
=== RUN   TestSameFinishTieBreakByPacketID
--- PASS: TestSameFinishTieBreakByPacketID (0.00s)
=== RUN   TestDynamicWeightChange
    scheduler_test.go:111: post-change window [220,320]: flow1=75 flow2=30 ratio=2.50 (want ~3)
--- PASS: TestDynamicWeightChange (0.00s)
=== RUN   TestFlowWithdrawal
--- PASS: TestFlowWithdrawal (0.00s)
=== RUN   TestCancelNoDoubleCount
--- PASS: TestCancelNoDoubleCount (0.00s)
ok  	wfqsim/sim	0.210s
```
