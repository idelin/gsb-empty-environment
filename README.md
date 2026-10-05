# dedup

Go 块级去重库：弱摘要分桶、强摘要 + 长度 + 全内容比较确认相等，事务化引用计数。

## 设计

- **分桶与确认**：块先按弱摘要（Adler-32）分桶快速定位候选，再用强摘要
  （SHA-256）+ 长度 + 全内容比较确认相等。内容比较是最终仲裁者。
- **故障隔离**：若注入故障使不同内容产生相同强摘要，新内容会被隔离到
  `strong#vN` 变体键下并记录 `Conflict`，两份数据都保留，绝不静默覆盖。
  变体编号按强摘要全局分配，弱摘要故障同样安全。
- **事务提交**：`Commit(txID, obj)` 原子地写入块并递增引用计数；任一步
  失败即逆序回滚（新建块删除、既有块计数复原）。同一 `txID` 重复提交是
  幂等的，返回首次结果并置 `Duplicate`。
- **负计数防护**：`Release` 先整体校验再扣减，任何会导致计数为负的释放
  都被拒绝且不产生部分效果。
- **删除/引用竞争**：`Sweep` 与 `Commit` 共用同一把锁——被清扫的块若被
  并发引用，要么计数已提升、要么由 `put` 原样重建，内容寻址保证语义不变。
- **独立重算**：`Recount` 从对象根出发重新计算全部引用计数并修复漂移，
  同时报告对象引用了但已缺失的块。

## 用法

```go
s := dedup.New()
res, _ := s.Commit("tx-1", dedup.Object{Root: "obj-1", Blocks: [][]byte{a, b}})
res2, _ := s.Commit("tx-1", dedup.Object{Root: "obj-1", Blocks: [][]byte{a, b}}) // res2.Duplicate == true
blocks, _ := s.Get("obj-1")
_ = s.Release("obj-1")
collected := s.Sweep()
report := s.Recount()
```

故障注入（测试用）：`WithStrongHash` / `WithWeakHash` 替换哈希函数，
`SetCommitFailHook` 在指定提交步骤注入失败以验证回滚。

## 测试

```sh
go test -race ./...
```

覆盖：重复提交幂等、强摘要冲突隔离、提交中途失败回滚、负引用计数防护、
块删除与新引用竞争、从对象根独立重算引用数。
