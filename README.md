# ARIES 崩溃恢复引擎 + 细粒度度量监控（TypeScript）

零依赖 TypeScript 实现：数据库经典 **ARIES** 预写日志（WAL）崩溃恢复协议
（Analysis / Redo / Undo 三阶段 + CLR 补偿日志），并内置高精度时序度量
采集、统计聚合与多格式导出。

## 运行环境

- Node.js **>= 22.6**（使用内置 `--experimental-strip-types` 直接运行 TypeScript，
  无需编译步骤、无任何第三方依赖）
- 测试使用 Node 内置测试运行器 `node:test`

```bash
# 运行全部测试
node --experimental-strip-types --test tests/*.test.ts
# 或 npm test

# 运行端到端演示（构造 WAL → 崩溃 → 三阶段恢复 → 度量导出）
node --experimental-strip-types examples/demo.ts
# 或 npm run demo
```

## 架构

```
src/
├── aries/
│   ├── types.ts      # 日志记录类型（UPDATE/CLR/COMMIT/END/CHECKPOINT）、错误码
│   ├── crc32.ts      # CRC-32 校验 + 确定性序列化（WAL 记录防撕裂）
│   ├── wal.ts        # WalWriter（同步追加，LSN 分配）/ readWal（容错顺序读）
│   ├── storage.ts    # DiskStorage（持久页存储）+ BufferPool（LRU、recLSN 跟踪、命中率）
│   ├── database.ts   # 事务运行时：begin/update/commit/checkpoint/crash
│   └── recovery.ts   # RecoveryEngine：Analysis → Redo → Undo
└── metrics/
    ├── registry.ts   # Counter / Gauge / Histogram（纳秒计时）、注册表
    ├── aggregator.ts # 时间窗口聚合：min/max/mean/stddev/p50-p99、速率、命中率
    └── exporter.ts   # JSON / Prometheus 文本导出，写文件
```

## ARIES 三阶段

1. **Analysis**：从最近一个完整 checkpoint（END_CHECKPOINT 携带 TT/DPT 快照）
   起扫描，重建 TransactionTable 与 DirtyPageTable，确定 redoLSN（DPT 最小 recLSN）。
2. **Redo**：历史重复。从 redoLSN 前向扫描，对 DPT 中 recLSN ≤ 记录 LSN 且
   pageLSN < 记录 LSN 的页重放 UPDATE/CLR 后像（含失败事务的修改）。
3. **Undo**：以最大堆按 LSN 逆序回滚所有 loser 事务，每个撤销动作写入 CLR
   （携带 undoNextLSN，崩溃重启后不会重复撤销），事务链结束时写 END 记录。

WAL 每条记录带 CRC-32 校验；崩溃造成的撕裂尾部被安全截断并按错误码计数
（`WAL_CORRUPT_RECORD` / `WAL_CHECKSUM_MISMATCH` / `WAL_UNKNOWN_TYPE`）。

## 度量指标

| 指标 | 类型 | 说明 |
| --- | --- | --- |
| `aries_phase_duration_ns{phase}` | Histogram | 各阶段纳秒级耗时分布 |
| `aries_log_records_scanned_total{phase}` | Counter | 各阶段扫描日志数 |
| `aries_redo_records_total{result}` | Counter | Redo 应用/跳过明细 |
| `aries_undo_clr_total` | Counter | Undo 写出的 CLR 数 |
| `aries_errors_total{code}` | Counter | 按错误码分类的异常 |
| `aries_analysis_*_size` | Gauge | Analysis 后 TT/DPT 内部状态 |
| `buffer_pool_hits/misses_total` | Counter | 缓冲池命中率来源 |

聚合器按需计算时间窗口内的 count/min/max/mean/stddev/p50/p90/p95/p99、
每秒速率与命中率（0/0 返回 `null` 而非 `NaN`）；导出支持 JSON 与
Prometheus 文本格式。度量系统不持有任何定时器或句柄，`close()` 后状态
完全释放（测试严格断言无泄漏）。

## 测试

`tests/recovery.test.ts`：验收场景（3 个未提交 + 2 个已提交事务，
checkpoint 后崩溃）验证三阶段正确性、恢复幂等、pageLSN 跳过、WAL 损坏
容错与错误码统计、无 checkpoint 全量扫描。

`tests/metrics.test.ts`：计数器/仪表/直方图边界（空快照、单样本、桶边界
含等号、非法输入）、分位数插值、时间窗口过滤、命中率 0/0 边界、JSON 与
Prometheus 导出（含标签转义）、64 路并发异步写入无丢失、关闭后无句柄/
定时器泄漏。
