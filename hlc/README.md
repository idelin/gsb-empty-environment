# 持久化混合逻辑时钟（HLC）

多机审计服务使用的可恢复混合逻辑时钟组件，为跨节点事件分配全序、可持久恢复的
时间戳。

## 时间戳结构

`Timestamp` 由三部分组成，按 `(Physical, Logical, NodeID)` 字典序全序比较：

- `Physical int64`：有符号 64 位 Unix 毫秒（支持 1970 之前的纪元）。
- `Logical uint16`：16 位逻辑计数，区分同一物理毫秒内的事件，范围 `0..65535`。
- `NodeID uint64`：铸出该时间戳的节点 ID，作为最终决胜键，保证时间戳全局唯一可比。

`MarshalBinary` / `UnmarshalBinary` 使用固定 18 字节大端编码（8+2+8）。

## 因果序规则

- 本地事件 `Now`：取 `max(wall, last)`；物理分量不前进时逻辑计数递增。
- 接收事件 `Receive`：取 `max(wall, last, remote)`，遵循标准 HLC 三分支规则，
  保证接收时间戳严格晚于远端时间戳和此前所有本地时间戳。
- 物理时钟回拨时，仍以已落盘的物理下界为准，时间戳不会倒退。

## 单调下界与崩溃恢复

- 每个对外返回的时间戳都先通过 `Store.Save` 持久化，成功后才返回。
- 重启时 `New` 从 `Store.Load` 恢复下界，持久化状态始终优先于物理时钟，
  因此不会重发任何已经返回过的值。
- 落盘失败时回滚内存状态并丢弃候选值，未持久化的值永远不会返回；下一次成功
  调用产生的是紧接其后的值，不会跳号。
- “落盘成功 → 对外返回”之间允许崩溃：重启后从持久化下界继续，崩溃窗口中已
  持久化的值不会被重新发放（见 `TestCrashBetweenSaveAndReturn`）。

`FileStore` 采用“临时文件 + fsync + 原子 rename + 目录 fsync”，崩溃只会留下
完整的旧状态或新状态，不会出现撕裂文件。测试另有可注入故障的 `MemStore`。

## 计数器溢出与禁止持锁等待

同一物理毫秒耗尽 16 位计数时，时钟等待物理时间推进到下一毫秒后重试。等待通过
`PhysicalClock.WaitUntil` 在**释放时钟锁之后**进行（`waitForPhysical`），其他
goroutine 仍可推进；测试用 `mu.TryLock` 断言等待期间锁是空闲的。

## 远端漂移保护

当远端时间戳的物理分量领先本地 wall 超过 `MaxDrift`（默认 1000ms）时，`Receive`
返回包装 `ErrRemoteAhead` 的错误且**不修改任何本地状态**，防止超前节点污染本地
时钟。

## 测试中的伪时钟与可控调度

- `FakeClock` 是手动控制的物理时钟，支持前进、回拨，以及可被测试观测的
  `WaitUntil` 等待，用于确定性复现计数器溢出、同刻并发。
- `Controller`（测试辅助）在命名钩子点 `enter/locked/before_wait/after_wait/
  saved/rejected` 停车，按 `(opID, step)` 精确放行，从而比较任意接收/本地事件
  交错下的因果单调性。
- `TestCausalMonotonicityArbitraryInterleavings` 用 40 个随机提交顺序运行同一组
  本地/接收操作，并与单线程参考模型逐值比对，同时校验严格递增、无重复、接收
  晚于远端。
- 落盘失败、崩溃窗口、重启不重发均有专门测试覆盖。

## 最小用法

```go
store := hlc.NewFileStore("/var/lib/audit/hlc.state")
clk, err := hlc.New(hlc.Config{
    NodeID:   nodeID,
    Physical: hlc.NewSystemClock(),
    Store:    store,
})
if err != nil { /* ... */ }

sendTS, err := clk.Now(ctx)
recvTS, err := clk.Receive(ctx, remoteTS)
```
