# swq — 滑动时间窗精确分位数维护器

基于**有序统计跳表**（span 跳表，Redis zskiplist 风格）+ **过期最小堆**的
滑动窗口精确分位数计算器。纯 Go 标准库实现，无任何批量全量排序：
插入、删除、按秩查询均为 O(log n) 增量操作。

## 设计

- `skipList`：每个节点带 `span`（到下一节点的底层步数），支持 O(log n)
  按秩定位第 k 小元素；元素按 `(value, id)` 排序，相等值按事件 ID 区分，
  因此重复值被正确去重为独立事件。
- `expiryHeap`：按时间戳的最小堆驱动过期；窗口时钟只随数据前进
  （`Add` 中的最大时间戳或显式 `Advance`），乱序插入安全。
- 窗口语义：半开区间，事件 `ts` 存活当且仅当 `ts + window > now`；
  边界相等（`ts + window == now`）的事件被驱逐。
- 分位数定位：`rank = floor(q * (n-1))`，返回排序后该秩的值。
- 单条撤回：`Remove(id)` 从跳表精确删除该事件；堆中残留项惰性跳过。

## API

```go
q := swq.New(10 * time.Second)
id := q.Add(9.0, ts)   // 插入（可乱序），返回事件 ID
q.Advance(now)         // 推进窗口时钟并过期
q.Remove(id)           // 撤回单条事件
v, err := q.Quantile(0.5) // 精确 p50，rank = floor(0.5*(n-1))
n := q.Len()
```

## 运行

```sh
export GO111MODULE=on
go test -v ./...   # 测试
go run ./cmd/demo  # 演示
```

## 验收

- 窗口含 `[1, 1, 9, 20]` 时 `q=0.5` → `1`（rank = floor(0.5*3) = 1）
- 撤回一条 `1` 后 `[1, 9, 20]`，`q=0.5` → `9`（rank = floor(0.5*2) = 1）
- 相等值事件各自独立、逐条撤回；边界相等事件精确过期
- 5000 步随机差分测试（乱序插入/撤回/推进时钟）对照暴力排序模型
