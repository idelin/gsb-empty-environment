# skiplist — 并发内存跳表（MemTable）与只读快照迭代器

基于多层随机链表的内存有序表，仅使用 Go 标准库实现。

## 设计

- **多层随机链表**：最大 20 层，节点以 1/4 概率逐层晋升；随机层数由原子计数器 + 位混合生成，无全局锁。
- **无锁层级索引**：所有层间指针通过 `sync/atomic` 的 `LoadPointer` / `CompareAndSwapPointer` 维护。插入时先在第 0 层 CAS 链接（成功即对外可见），再自底向上链接更高层，冲突时重新定位前驱后继重试，全程无互斥锁。
- **点查/更新**：`Get` 沿层级索引查找；相同 key 的 `Set` 通过原子替换 value 指针完成更新。
- **只读快照迭代器**：`NewIterator(start, end)` 在创建时沿第 0 层物化 `[start, end)` 区间内的全部键值对（值做深拷贝），之后的并发插入/覆写不会影响本次遍历结果，实现完全稳定隔离。

## API

```go
sl := skiplist.New()
sl.Set(key int64, value []byte)          // 并发安全插入/更新
v, ok := sl.Get(key int64)               // 点查
it := sl.NewIterator(start, end int64)   // [start, end) 只读快照迭代器
for it.Next() { it.Key(); it.Value() }   // 严格单调递增遍历
```

## 运行样例

```
$ go run ./cmd/demo
并发插入完成: 10000 个键值对
区间 [4995, 5005) 扫描: 4995 4996 4997 4998 4999 5000 5001 5002 5003 5004
快照迭代器输出 10000 个键（写入期间保持稳定）
```

## 测试结果

```
$ go test -race -v ./...
=== RUN   TestConcurrentInsertTenThousand
--- PASS: TestConcurrentInsertTenThousand (0.93s)
=== RUN   TestRangeIteratorMonotonic
--- PASS: TestRangeIteratorMonotonic (0.47s)
=== RUN   TestIteratorSnapshotIsolation
--- PASS: TestIteratorSnapshotIsolation (0.70s)
=== RUN   Example
--- PASS: Example (0.00s)
PASS
ok  	example.com/skiplist	4.942s
```

测试覆盖三条验收边界：

1. `TestConcurrentInsertTenThousand`：8 个 goroutine 并发随机插入 1 万个整数键值对，全量扫描严格单调递增、数量完整、逐键点查命中。
2. `TestRangeIteratorMonotonic`：任意区间（含空区间、越界起点、尾部区间）迭代器输出严格单调递增且与期望区间完全一致。
3. `TestIteratorSnapshotIsolation`：迭代遍历期间 4 个 goroutine 持续插入 1 万个新键并覆写旧键，快照迭代器输出结果集与创建时完全一致；写入结束后新建迭代器可见全部 2 万个键。
