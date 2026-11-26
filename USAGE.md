# RadixKV 使用与验收说明

根目录是可导入的 Go 包 `radixkv`：

```go
import radixkv "prescreen.local/ps-fd9b22029b"

cache := radixkv.NewRadixCache(totalBlocks, blockSize)
scheduler := radixkv.NewScheduler(totalBlocks, blockSize, maxTokensPerStep)

// 或让调度器绑定已有缓存：
scheduler = radixkv.NewSchedulerWithCache(cache, maxTokensPerStep)
```

## 组件

- `node.go`：基数树节点、token 序列与 `BlockSlice` 物理块区间切分。
- `pool.go`：`free/tree/running` 物理块状态机和 owner 校验。
- `cache.go`：块级前缀匹配、边中途原子劈分、祖先路径引用计数、LRU 可驱逐叶最小堆。
- `scheduler.go`：单步混批调度、解码优先、公平切分 Chunked Prefill 预算、驱逐和抢占。
- `acceptance_test.go`：题目要求的五类验收测试。
- `internal_edge_test.go`、`robustness_test.go`：非对齐劈分、抢占排序和确定性矩阵。

## Radix Cache

```go
cache := radixkv.NewRadixCache(64, 16)
_, _ = cache.Insert([]int{1, 2, 3, 4, 5, 6, 7, 8})

match := cache.MatchPrefix([]int{1, 2, 3, 4, 99, 100})
_ = match.CachedTokens // 按 blockSize 对齐的可跳过 token 数

_, _ = cache.LockPrefix("request-1", []int{1, 2, 3, 4})
defer cache.UnlockRequest("request-1")

evicted := cache.EvictLRU()
_ = evicted
```

关键语义：

- 劈分边时创建新的前缀节点，原节点保留为后缀节点，避免失效的请求锁指针。
- 新前缀节点继承原边 `RefCount`，后缀节点保持原计数；旧请求的祖先路径因此仍然守恒。
- 非块对齐切分通过同一物理块的两个相邻 `BlockSlice` 表达；物理块只计数一次，等所有树引用释放后才空闲。
- 只有 `RefCount == 0` 的叶子可驱逐。父节点在最后一个子节点被驱逐、自身成为叶子且引用为零时立即进入 LRU 堆。
- 堆项带时间戳和版本号，采用惰性删除处理访问更新和失效状态。

## Scheduler

```go
scheduler := radixkv.NewScheduler(128, 16, 2048)
scheduler.AddRequest("req-1", promptTokens, 128)

for !scheduler.AllDone() {
    step, err := scheduler.Step()
    if err != nil {
        return err
    }

    _ = step.DecodeTokens
    _ = step.PrefillTokens
    _ = step.SkippedCachedTokens
    _ = step.EvictedNodeIDs
    _ = step.PreemptedRequestIDs

    if !step.Conservation.Conserved {
        panic(step.Conservation.Problems)
    }
}
```

每步顺序：

1. 最老到最新遍历在途 Decode 请求；预算允许时每人推进 1 token。
2. 剩余 `maxTokensPerStep` 预算通过轮转公平分配给 Prefill 请求，长 Prompt 可跨多步。
3. 完整命中前缀块直接移动 `PrefillCursor`，不消耗 Prefill 计算预算。
4. 申请块时先分配空闲块，再驱逐 LRU 叶子。
5. 缓存耗尽后抢占优先级最低、同优先级中最近到达的运行中请求；最老 Decode 请求受保护。
6. 被抢占请求解锁前缀路径、释放全部未挂树块并回到 Waiting；本步不再重新接纳该请求。

如果最老 Decode 请求自己就是唯一运行请求，且池中的树块加它的独占块已经占满 `totalBlocks`，则任何策略都无法让更长序列继续；此时 `Step` 返回 `ErrKVCapacityExhausted`，而不是死锁或破坏块守恒。

## 守恒审计

```go
report := scheduler.Conservation()
// report.TreeBlocks + report.FreeBlocks + report.RunningBlocks == report.TotalBlocks
```

`RunningBlocks` 即未挂树运行块。审计还会检查：

- 请求 `BlockIDs` 不重复；
- 每个运行块的 pool owner 与请求 ID 一致；
- tree 遍历能找到 pool 标记的所有树块；
- 非活动请求不残留运行块。

## 测试

```bash
go test ./...
go test -race ./...
go test ./... -run TestAcceptance -v
```
