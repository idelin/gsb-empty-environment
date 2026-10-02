# B+ 树范围扫描迭代器（Go）

磁盘导向的 B+ 树，页由固定容量的 BufferPool 管理（Pin/Unpin + LRU 淘汰）。
范围扫描迭代器定位起始 Key 所在叶子与槽位后，沿叶子兄弟链表顺序前进；
跨越物理页边界时自动 Pin 新页、Unpin 旧页，任意时刻最多持有一页 Pin。

## 结构

- `bufferpool.go` — 固定帧数缓冲池；全帧被 Pin 时返回 `ErrPoolExhausted`（防内存溢出）；`TotalPins()` 用于泄漏检测
- `btree.go` — B+ 树插入与页分裂；叶子页通过 `next` 指针构成有序链表
- `iterator.go` — `Scan(lo, hi)` 半开区间 `[lo, hi)` 迭代器；页边界穿越时换 Pin；`Close()` 幂等
- `btree_test.go` — 验收测试与边界测试
- `cmd/demo/main.go` — 运行样例

## 运行样例

```
$ go run ./cmd/demo
inserted 10000 records, pins after load = 0
  key=2500 value=25000
  key=2501 value=25010
  key=2502 value=25020
  ...
  key=7497 value=74970
  key=7498 value=74980
  key=7499 value=74990
scanned 5000 records in [2500, 7500)
buffer pool: hits=27823 misses=480 evictions=472 outstanding-pins=0
```

## 测试结果

缓冲池仅 8 帧（远小于树的 ~170 页），强制发生页面淘汰以验证无泄漏、无溢出：

```
$ go test ./... -v
=== RUN   TestRangeScanAcceptance
=== RUN   TestRangeScanAcceptance/sequential-insert
    btree_test.go:98: records=5000 pool: hits=27823 misses=480 evictions=472 pins=0
=== RUN   TestRangeScanAcceptance/shuffled-insert
    btree_test.go:98: records=5000 pool: hits=17334 misses=10219 evictions=10211 pins=0
--- PASS: TestRangeScanAcceptance (0.01s)
    --- PASS: TestRangeScanAcceptance/sequential-insert (0.00s)
    --- PASS: TestRangeScanAcceptance/shuffled-insert (0.01s)
=== RUN   TestScanCrossesManyLeaves
--- PASS: TestScanCrossesManyLeaves (0.01s)
=== RUN   TestScanEmptyAndEdgeRanges
--- PASS: TestScanEmptyAndEdgeRanges (0.00s)
=== RUN   TestIteratorEarlyClose
--- PASS: TestIteratorEarlyClose (0.00s)
=== RUN   TestPoolExhaustionGuard
--- PASS: TestPoolExhaustionGuard (0.00s)
PASS
ok  	bptree	0.350s
```

验收点：10000 条连续记录；`Scan(2500, 7500)` 输出恰好 5000 条严格递增、
不重复的记录；扫描结束后 `TotalPins() == 0`（无页面泄漏）；8 帧小池全程
未触发 `ErrPoolExhausted`（无内存溢出）。
