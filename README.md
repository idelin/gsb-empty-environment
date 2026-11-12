# 握手公平超时语义

认证模型中的超时动作如果被其他消息无限推迟，会让本应拒绝的会话永久保留。实现供Go验证宿主调用的计时调度包，时间推进与零耗时消息分开，截止点一到超时事件必须优先于普通事件；搜索是否存在无限零耗时循环阻止时间前进，并给循环证据。对显式允许的最大零时步数作展开，用小模型验证到期同刻与持续重传。

## 组件

- `scheduler.go`：虚拟时钟调度器。`Advance`/`AdvanceToNext` 是唯一的时间
  推进入口；`Post`/`PostAt` 只产生不耗时的普通事件。堆按
  `(时刻, 类型, 入队序号)` 排序，同刻 `TimeoutEvent` 恒先于 `NormalEvent`。
  相邻两次成功推进之间的零耗时步数受 `MaxZeroSteps` 约束，触顶返回
  `ErrLivelock`，未到期超时仍保留在队列中。
- `search.go`：`SearchZeroCycle` 在冻结时钟下反复派发零耗时事件，用宿主
  提供的状态指纹检测重复，输出含前缀、循环体、入口指纹和冻结时刻的
  `CycleEvidence`。
- `model.go`：认证小模型（`World`/`Session`/`Config`）。含整体截止
  `auth.deadline`、固定间隔持续重传 `auth.retransmit`，以及可开关的
  “反射型无关消息”——它在同一时刻自我再生产，用于构造零耗时循环。

## 关键性质（由测试固定）

- 到期同刻优先：tick 5 的截止超时先于同刻普通消息派发并拒绝会话
  （`TestTimeoutPriorityAtSameInstant`、`TestModelDeadlineWinsAtExpiry`）。
- 时间与零耗时分离：普通消息不移动时钟，只有显式推进才移动
  （`TestZeroStepsDoNotAdvanceClock`）。
- 无限零耗时循环证据：反射型 `net.junk` 在冻结时钟 tick 0 上形成自环，
  指纹重复被 `SearchZeroCycle` 捕获（`TestInfiniteZeroCycleReflectedJunk`，
  证据经 `t.Log` 打印）；同一链路也会被 `MaxZeroSteps` 预算截断
  （`TestSameCycleCaughtByBudget`、`TestLivelockBudgetExhausted`）。
- 持续重传与活性：截止前按间隔重传，截止时刻拒绝并停止重传
  （`TestPersistentRetransmit`）；有限零耗时链在预算内排空后仍能推进到
  截止并拒绝（`TestBoundedUnfoldReachesDeadline`）。

## 运行

```sh
go vet ./...
go test -race ./...
```
