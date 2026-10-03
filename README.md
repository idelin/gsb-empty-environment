# ARIES 崩溃恢复引擎 + 细粒度度量监控（Go，仅标准库）

实现数据库经典 ARIES 崩溃恢复协议的三阶段扫描，并对内部关键状态、
延迟分布、命中率与错误码做高精度时序度量采集与聚合导出。

要求 Go 1.24+（仅标准库，无第三方依赖）。

## 模块

- `wal/` — 预写日志：记录类型（UPDATE/CLR/COMMIT/ABORT/END/CHECKPOINT）、
  带 CRC32 校验的帧格式、顺序写/追加写/顺序读、`Builder` 维护 PrevLSN 链并生成检查点。
- `recovery/` — ARIES 三阶段引擎：
  - **Analysis**：从最近 Checkpoint 起扫描，重建 `DirtyPageTable` 与 `TransactionTable`；
  - **Redo**：历史重复，从 DPT 最小 recLSN 起按 `pageLSN < rec.LSN` 精确重放；
  - **Undo**：按 LSN 最大堆反向扫描 loser 事务，逐条写 CLR（含 UndoNext），
    收尾写 END，winner 事务补写 END。
- `buffer/` — 缓冲池：页表 + FIFO 置换 + 脏页写回，命中/未命中计数，
  `Crash()` 丢弃内存态模拟崩溃，`HitRate()` 输出命中率。
- `metrics/` — 度量注册表：Counter/Gauge、固定桶 + 精确分位数（p50/p90/p99）
  的 Histogram、分类错误码计数、`Timer` 延迟采集，JSON 与文本（Prometheus 风格）导出。
  全部操作并发安全（atomic / mutex）。

## 运行样例

```sh
go run ./cmd/ariesdemo
```

构造 5 个事务（T1、T2 已提交，T3、T4、T5 未提交）+ Checkpoint 后崩溃，
执行三阶段恢复并打印恢复报告、落盘页面、缓冲池命中率与完整度量导出。

## 测试

```sh
go test ./...        # 单元 + 验收测试
go test -race ./...  # 并发安全（data race）验证
```

覆盖：验收场景（3 未提交 + 2 已提交、Checkpoint 后崩溃，断言已提交数据
持久化、未提交修改被 CLR 回滚）、无 Checkpoint/空 WAL/撕裂写尾部（错误码
`WAL_CORRUPT`）、直方图空/单样本/桶边界/分位数、导出格式、32 goroutine
并发度量采集，以及 goroutine 无泄漏断言。
