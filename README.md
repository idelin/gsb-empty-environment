# ARIES 崩溃恢复引擎 + 形式化约束巡检仪（Go，仅标准库）

实现经典 ARIES 崩溃恢复协议的三阶段扫描，并附带一个离线完整性巡检仪，
对 WAL 与磁盘页状态做形式化不变式断言。

## 组件

| 文件 | 职责 |
|---|---|
| `wal.go` | 追加式 WAL：UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT 记录，二进制帧序列化，LSN = 文件偏移（8 字节魔数头占用偏移 0，使 0 可安全作为空链哨兵），`Append`/`Records` 互斥并发安全 |
| `storage.go` | 页式磁盘管理器（256B 页 = 8B pageLSN + 248B 负载）与模拟缓冲池（DPT/recLSN 跟踪、steal 冲刷） |
| `simulator.go` | 工作负载模拟器：事务 Begin/Update/Commit/Abort、Checkpoint（ATT+DPT 快照），丢弃模拟器即模拟崩溃 |
| `recovery.go` | 恢复引擎：Analysis（从最近 Checkpoint 重建 ATT/DPT）→ Redo（从最小 recLSN 重复历史，pageLSN 幂等）→ Undo（按全局 LSN 降序回滚 loser，逐条写 CLR，undoNext 跳过已补偿记录，链尾写 END） |
| `inspector.go` | 形式化约束巡检仪（见下） |
| `cmd/demo` | 验收场景运行样例 |

## 巡检仪断言的不变式

- `LSN_ORDER` / `DUPLICATE_LSN`：LSN 严格递增且唯一
- `DANGLING_REF`：PrevLSN / UndoNextLSN / Checkpoint ATT·DPT 中的引用必须存在
- `CROSS_TXN_REF`：链指针不得跨事务
- `ORPHAN_LOOP`：Prev/UndoNext 链必须严格递减且无环（含 CLR 互指环）
- `ORPHAN_RECORD`：事务每条记录必须从事务链尾可达（无孤立记录）
- `CHECKPOINT_REF`：Checkpoint 快照的 ATT lastLSN / DPT recLSN 必须指向过去且类型、页号一致
- `UNTERMINATED_TXN`（恢复后）：每个事务必须有 END，严格无泄漏
- `DATA_SKEW`（恢复后）：磁盘 pageLSN 必须等于该页 WAL 前沿（防丢失更新与幻影写）

## 运行

```sh
go test -race -v ./...   # 测试（含竞态检测）
go run ./cmd/demo        # 端到端样例
```

## 验收场景（cmd/demo）

5 个事务：T1 在 Checkpoint 前提交，T2 在 Checkpoint 后提交（页未冲刷），
T3/T4/T5 未提交即崩溃（T3、T4 的脏页被 steal 到磁盘，T4 还覆盖了 T1 已提交的数据）。
恢复后：已提交数据全部持久化，未提交修改全部被 CLR 补偿回滚，
巡检仪断言零违规。实测输出：

```
=== Phase 1: Analysis ===
losers: [2 4 5]
DPT: page=4 recLSN=1216, page=3 recLSN=1861, page=1 recLSN=2519, ...
*** CRASH *** → Redo 重复历史 → Undo 写 4 条 CLR + 3 条 END
=== Final disk state ===
  page 1: pageLSN=5323 committed-by-T1     ← T4 的覆盖被撤销
  page 3: pageLSN=1861 committed-by-T2     ← 未冲刷的已提交数据被 Redo 找回
  page 4/5/6: (empty)                      ← 未提交数据全部回滚
=== Formal constraint inspection (post-recovery) ===
INSPECTION PASSED: all invariants hold, no leaks
```

## 测试结果（go test -race）

19 个测试全部通过：端到端崩溃恢复、Analysis 表重建、恢复幂等性、
中止事务回滚、pageLSN 幂等 Redo，以及巡检仪全部边界条件
（悬挂引用、跨事务引用、孤立环路、孤立记录、Checkpoint 引用、
未终止事务、数据倾斜 stale/phantom）与并发追加+并发巡检的竞态安全。
