# skiplist — 并发内存跳表（MemTable）与只读快照迭代器

基于多层随机链表的有序内存表，仅使用 Go 标准库。

## 设计

- **无锁层级索引**：每个节点的各层前驱指针为 `atomic.Pointer[node]`；
  插入通过 CAS 将新节点逐层拼接，读者全程原子加载、不加任何锁。
- **快照隔离**：节点在底层链表链接完成后获得单调递增的序列号
  （线性化点）。迭代器创建时捕获序列号快照，遍历中只暴露
  `seq <= snap` 的节点，因此并发插入/更新完全不会泄漏进迭代结果。
- **范围扫描**：`NewIterator(start, end)` 返回闭区间 `[start, end]`
  上严格递增的只读游标（`Valid` / `Key` / `Value` / `Next`）。

## API

```go
sl := skiplist.New()
sl.Insert(key, value)            // 并发安全；重复 key 覆盖更新
v, ok := sl.Get(key)
n := sl.Len()
for it := sl.NewIterator(lo, hi); it.Valid(); it.Next() { ... }
```

## 运行

```sh
go test -race -v ./...   # 验收测试（含 1 万随机并发插入、区间单调性、写期间快照隔离）
go run ./cmd/example     # 运行样例
```
