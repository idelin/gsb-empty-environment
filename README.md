# cdcundo — 反向补偿重放与数据回滚包生成器

基于 CDC 前置/后置镜像（Before/After Image）生成反向 Undo 脚本的灾备引擎。
捕获每条变更的完整镜像，在人为误操作后逆向生成补偿 DML 序列，实现快速冲正。

## 核心设计

- **CDC 事件模型**（`cdc.go`）：`ChangeEvent` 携带 `TxnID / Seq / Table / Op / Before / After / At`，
  以 JSON Lines 落盘，支持 `ReadLog` 按序读回并校验镜像完整性。
- **反向补偿映射**（`compensate.go`）：
  - `INSERT → DELETE`（按 After Image 主键删除）
  - `DELETE → INSERT`（按 Before Image 整行还原）
  - `UPDATE → UPDATE`（仅将变更列恢复为 Before Image 的值，按主键定位）
- **LIFO 重放顺序**：补偿语句按源事件逆序排列，同一行多次变更时镜像逐层还原。
- **回滚包**（`RollbackPackage`）：有序补偿语句 + 元信息，`SQL()` 渲染为
  包裹在 `BEGIN/COMMIT` 中、字面量已转义的可独立执行 Undo 脚本。
- **演示存储**（`store.go`）：带 CDC 捕获能力的内存表，写入即记录 Before/After Image，
  `Replay` 模拟在数据库上执行回滚包。

## 运行演示

```sh
cd cmd/demo && go run main.go
```

输出：

```
[1] 初始化 1000 条用户数据完成
[2] 误操作 UPDATE 影响 1000 行，email/phone 已被清空
[3] 从 CDC 日志读取 2000 条事件，定位误操作事务 1000 条
[4] 生成回滚包：1000 条补偿 UPDATE，已写入 undo.sql
[5] 恢复完成：1000 行全部还原至误操作前状态，耗时 762ms
[6] 校验通过：恢复后数据与基线快照逐行一致 ✔
```

生成的 `undo.sql` 片段：

```sql
BEGIN;
-- undo txn=txn-bad-0001 seq=2000 op=UPDATE at=2026-10-02T06:18:33.257Z
UPDATE `users` SET `email` = 'user0999@example.com', `phone` = '13800000999' WHERE `id` = 999;
...
COMMIT;
```

## 测试

```sh
go test ./... -v
```

10 个测试全部通过，覆盖：

- `TestInverseUpdateRestoresBeforeImage` — UPDATE 反转为 Before Image 反向赋值
- `TestInverseInsertBecomesDelete` / `TestInverseDeleteBecomesInsert` — 三种操作映射
- `TestInverseRejectsInvalidEvents` — 残缺镜像 / 未知操作 / 未配主键的报错路径
- `TestRollbackPackageIsLIFO` — 同行多次变更的逆序还原
- `TestSQLScriptRendering` — Undo 脚本事务包裹与单引号转义
- `TestThousandRowRecovery` — **验收场景**：1000 行被清空后生成 1000 条补偿语句并逐行校验恢复
- `TestCDCLogRoundTrip` / `TestReadLogCorrupted` — 日志序列化往返与损坏检测
- `TestRollbackOnlyBadTxn` — 混合事务中只冲正误操作事务

仅使用 Go 标准库，兼容 Go 1.11+。
