# cowtree — 只读快照遍历 + 页级 Copy-On-Write（Go，仅标准库）

一个 B+ 树存储内核，演示无锁只读快照与页级 CoW 写入。

## 设计

- **页不可变**：节点页（`leaf` / `internal`）一旦发布就不再修改。
- **CoW 写事务**：`Put` 沿 root→leaf 路径复制经过的每一页，在副本上应用
  修改/分裂，最后用一次原子写替换根指针（`atomic.Pointer`）。旧版本页面
  由 Go GC 在无人引用后回收。
- **无锁只读快照**：`Snapshot()` 只是 O(1) 地读取当前根指针。由于从该根
  可达的所有页面都不可变，只读事务无需任何锁即可遍历历史一致版本，
  即使此时写事务正在分裂节点、逐层替换父指针。
- **单写者**：`wmu` 互斥锁串行化写事务；读者数量不限且从不阻塞写者。

## 文件

- `cowtree.go` — 实现（`Tree` / `Snapshot` / CoW insert+split）
- `cowtree_test.go` — 正确性与并发验收测试
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test -race -v ./...   # 测试（含并发验收）
go run ./cmd/demo        # 运行样例
```

## 验收边界

`TestConcurrentSplitScan`：预置 1000 个键后固定旧 Root 快照；写事务并发
插入 20000 个键（`MaxEntries=8`，持续触发页分裂与根替换）；4 个读者在
此期间反复全表扫描旧快照，逐键断言视图与写操作开始前完全一致。测试在
`-race` 下通过：无撕裂读、无丢失/多余键、无数据竞争。
