# 内存标签轮换隔离

检测版分配器用有限标签识别悬空指针，标签过快复用会让旧指针重新有效。写供Go内存宿主调用的分配规划包，每个地址段保留近期释放标签的隔离期，分配只能选未隔离标签，耗尽则等待或明确拒绝，不能悄悄复用；相邻段合并与拆分必须保留历史。用小标签域和虚拟时钟穷举验证部分区间重用及过期同刻的决定。

## 实现语义

- 地址区间均为左闭右开 `[Start, End)`，标签域为 `0..LabelCount-1`。
- 释放记录绑定到实际释放的子区间，而不是整块地址。标签只在与其释放区间重叠的新分配上被隔离；同一大段内互不重叠的部分可以立即复用该标签。
- 隔离期为 `[释放时刻, ExpiresAt)`。虚拟时间恰好等于 `ExpiresAt` 时标签已经可用，不需要多等一个 tick。
- 标签耗尽时，`RejectWhenExhausted` 返回 `StatusRejected`，`WaitWhenExhausted` 返回 `StatusWait`；两者都带最早的 `RetryAt`，且都不会签发分配。
- `Split` 将历史裁剪到两个子段，`Merge` 合并两侧历史；释放后自动合并相邻空闲段也走同一套历史保留逻辑。
- 所有带时间的调用共用单调虚拟时钟。时间回退会返回 `ErrClockRegression`；释放句柄会校验 ID、区间、标签和签发时刻，避免过期句柄释放新分配。

## 用法

```go
planner, err := netsec.New(netsec.Config{
    LabelCount: 4,
    Quarantine: 10,
    Exhaustion: netsec.WaitWhenExhausted,
})
if err != nil {
    return err
}
if err := planner.AddFree(netsec.Span{Start: 0x1000, End: 0x2000}); err != nil {
    return err
}

plan, err := planner.Allocate(now, netsec.Span{Start: 0x1000, End: 0x1040})
if err != nil {
    return err
}
switch plan.Status {
case netsec.StatusGranted:
    use(plan.Allocation.ID, plan.Allocation.Label)
case netsec.StatusWait:
    retryAt(plan.RetryAt)
case netsec.StatusRejected:
    reject(plan.Reason)
}
```

## 验证

`planner_test.go` 使用 1 到 3 个标签的小标签域、1 到 5 tick 的隔离期和虚拟时钟穷举检查：

- 隔离期内不可分配，以及 `ExpiresAt` 同刻可分配；
- 等待与拒绝两种耗尽策略及其 `RetryAt`；
- 所有小区间释放/请求组合下，标签隔离是否精确跟随地址重叠；
- 拆分边界两侧的历史裁剪，以及合并后历史覆盖的恢复。
