# gsb — 分支化写时复制字节序列存储

纯 Go 标准库实现。物理层是内容寻址的定长块存储（引用计数 + 字节账本），
逻辑层是不可变跨度链表示的分支版本，编辑全部在事务内原子完成。

## 设计

- **物理块**：每块负载 ≤ 64 字节，账本按 `16 字节头（id8+refs4+len4）+ 实际负载` 记账；
  按内容 SHA-256 去重，相同字节序列全仓只存一份。
- **版本/分支**：`[]Span{块ID, 偏移, 长度}`，编辑时切割头尾跨度零复制共享底块，
  仅新数据分配物理块（写时复制）；快照只复制跨度表。
- **事务**：所有引用计数变更记入撤销日志，`Commit` 生效；取消或物理配额超限
  整体回滚。事务期临时复制即时计入存活账本（`Tx.TempBytes` / `Store.LiveBytes`）。
- **移动**：源字节在任何修改前读出，重叠右值按操作前内容；
  目标落入源区间时插入点钳制到合法范围。
- **回收**：`Collect` 只回收引用计数为 0 的块；旧分支借用期间底块不可回收。
- **账本核对**：`VerifyLedger` 独立遍历所有存活分支的跨度引用，重建每块应有
  引用计数与存活字节数，与存储计数器逐一比对。

## 编码布局（小端序，真实字节占用）

```
仓库头:   magic[4]="GSB1" | quota i64 | nextID u64 | blockCount u32
块记录:   id u64 | refs u32 | dataLen u32 | data[dataLen]      （按 id 升序）
分支区:   branchCount u32
分支记录: nameLen u16 | name | length u64 | spanCount u32      （按名升序）
跨度记录: blockID u64 | off u32 | len u32
```

`Repo.EncodedSize()` 即该布局的实际字节数：重复内容只出现一条块记录，
逻辑增长只增加 16 字节/条的跨度记录（见 `TestEncodedDedupLinear`）。

## 运行

```sh
go test ./...     # 全部测试（含 4000 步随机对拍）
go run ./cmd/demo # 运行样例
```

## 运行样例输出

```
== 1. 构建逻辑序列（跨物理块） ==
逻辑长度: 220 字节, 物理块: 4 个, 存活物理占用: 284 字节
== 2. 分支快照 + 小编辑（写时复制） ==
事务内临时复制: 1 块 / 17 字节（已计入物理账本）
改 1 字节的物理增量: 17 字节（逻辑序列 220 字节，未整体复制）
== 3. 跨块移动（重叠右值按操作前内容） ==
移动后前 44 字节: 02|record-003|record-004|recorrecorX-000|rec
v1 分支不受影响，前 11 字节: record-000|
== 4. 配额超限整体回滚 ==
超额插入返回: gsb: 物理块配额超限
回滚后逻辑长度: 220（保持原值 220）
== 5. 借用期间底块不可回收 ==
v1 借用旧块期间 Collect: 回收 0 块 / 0 字节（旧块被借用，不可回收）
释放 v1 后 Collect: 回收 4 块 / 284 字节
== 6. 真实编码布局与占用字节 ==
编码占用: 569 字节（布局: 仓库头24 + 块记录(16+负载) + 分支区4 + 分支记录(14+名字) + 跨度记录16）
解码往返: 存活物理 330 字节, 账本核对: OK
== 7. 独立遍历核对账本 ==
存活 330 字节 / 垃圾 17 字节 / 编码 569 字节 — 账本一致
```

## 测试结果

```
--- PASS: TestEncodeRoundTrip        编码往返 + 确定性 + 账本核对
--- PASS: TestEncodedLayout          布局字节数手工核算
--- PASS: TestEncodedDedupLinear     重复内容编码只增跨度记录
--- PASS: TestDecodeCorrupt          截断/坏魔数拒绝
--- PASS: TestRandomOpsVsNaive       4000 步随机操作 vs 朴素字节串模型，逐步核账本
--- PASS: TestCrossBlockRead         全区间跨块读取
--- PASS: TestEndianCrossBlock       跨块 uint64/uint32 大小端
--- PASS: TestEmptySlice             空切片无越界
--- PASS: TestBranchIsolation        相交修改只影响目标版本
--- PASS: TestOverlapMove            重叠移动按操作前内容
--- PASS: TestRollbackCancel         取消整体回滚
--- PASS: TestQuotaRollback          配额超限整体回滚
--- PASS: TestBorrowPreventsGC       借用期间底块不可回收
--- PASS: TestSmallEditNoFullCopy    1 字节编辑仅新增 17 物理字节
--- PASS: TestTempAccountingDuringTx 事务内临时复制计入账本
--- PASS: TestPhysicalDedup          相同内容物理去重
--- PASS: TestCollectAfterOverwrite  无借用垃圾可回收
PASS
ok  	gsb
```
