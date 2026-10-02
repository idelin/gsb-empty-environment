# aries — ARIES 崩溃恢复引擎 + 细粒度度量监控（Go，仅标准库）

实现数据库经典 **ARIES** 崩溃恢复协议（Analysis / Redo / Undo 三阶段），
并内置高精度时序度量采集、统计聚合与导出。仅使用 Go 标准库。

## 模块结构

| 包 | 职责 |
|---|---|
| `wal` | 预写日志：7 类日志记录（UPDATE/COMMIT/ABORT/END/CLR/BEGIN/END_CHECKPOINT）、CRC32 帧校验、追加/刷盘/顺序与随机迭代、撕裂写截断 |
| `storage` | 页式磁盘管理器（pageLSN 持久化于页头）+ 时钟置换缓冲池，强制 **WAL-first** 规则 |
| `db` | 事务执行：Begin/Update/Commit/Abort（在线 CLR 补偿）、模糊检查点（END_CHECKPOINT 携带 TT+DPT 快照） |
| `recovery` | ARIES 三阶段：Analysis 重建 TT/DPT；Redo 从最小 recLSN 重复历史；Undo 按 LSN 逆序回滚 loser 并写 CLR，保证幂等 |
| `metrics` | 计数器/仪表盘/分桶延迟直方图（P50/P90/P99）/滑动窗口/错误码计数，JSON 与 Prometheus 风格文本导出，全程无锁泄漏、无后台 goroutine |

## 运行测试

```sh
go test ./...          # 全部单元测试 + 验收场景
go test -race ./...    # 并发安全（metrics 64 goroutine 竞争、缓冲池等）
go test -v ./recovery/ # 验收：3 未提交 + 2 已提交事务，checkpoint 后崩溃
```

## 运行样例

```sh
go run ./cmd/aries-demo
```

输出（节选）：

```
== crash: 2 committed txns (T1,T2), 3 losers (T3,T4,T5 aborting) ==

== analysis ==
  TT  T3 status=RUNNING lastLSN=567
  TT  T4 status=RUNNING lastLSN=638
  TT  T5 status=ABORTING lastLSN=912
  DPT page=1 recLSN=0
  DPT page=2 recLSN=311
  DPT page=3 recLSN=567

== recovered pages (read from disk) ==
  page 1: "T1-data" (want T1-data)
  page 2: "T2-data" (want T2-data)
  page 3: "" (want empty)

== metrics (text export) ==
aries_recovery_pages_redone 4
aries_recovery_clr_written 4
aries_recovery_txns_rolled_back 3
aries_recovery_redo_seconds{quantile="0.99"} 0.0005
buffer hit rate during recovery: 0.75
```

## 验收场景（recovery/recovery_test.go）

- T1 提交于 checkpoint 前；checkpoint 落盘 TT+DPT 快照并刷页。
- T2 提交于 checkpoint 后（Redo 必须重放）；T3、T4 未提交；T5 中止到一半崩溃（已有 1 条 CLR）。
- 崩溃前把含 loser 数据的页 1、2 强制刷盘，页 3 不刷（Redo 重建），WAL 全部 durable。
- 恢复后断言：页 1 = `T1-data`、页 2 = `T2-data`、页 3 全零；undo 新写 4 条 CLR、3 条 END；二次恢复幂等（0 条新 CLR）。
- 度量断言：三阶段 timer 各 1 次观测、`pages_redone>0`、`clr_written=4`、`txns_rolled_back=3`、TT/DPT gauge=3、错误码为空、无 goroutine 泄漏。
