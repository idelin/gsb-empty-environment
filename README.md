# vtimer

一次性虚拟定时器库：显式时钟步进，无 goroutine、无 sleep。

## 模型

- `Clock` 持有虚拟时间与全部定时器；`Advance(d)` 推进时钟，把到期事件移入就绪队列。
- `Next()` 从就绪队列领取（claim）下一个有效事件，是交付的线性化点。
- `Timer.Reset` / `Timer.Stop` 在持锁瞬间线性化：代际（generation）递增，
  旧代事件即使已入就绪队列、尚未领取，也会在领取时被丢弃。
- 相同到期时间按定时器创建序号触发；创建序号在 Reset 后保持不变。

## 示例

```go
c := vtimer.NewClock(time.Unix(0, 0))
t1 := c.NewTimer(time.Second)

c.Advance(time.Second) // t1 到期，进入就绪队列
t1.Reset(2 * time.Second) // 旧代事件失效，永不会被交付

c.Advance(2 * time.Second)
ev, ok := c.Next() // 仅交付新代事件，且只交付一次
```

## 测试

`go test ./...` —— 除定向用例外，`TestPermutations` 枚举
{advance, claim, reset, stop} 全部长度 ≤6 的操作序列（5461 条），
与独立参考模型逐条比对交付代际序列，并断言任何代际不会被交付两次。
