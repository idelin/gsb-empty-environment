# ARIES 崩溃恢复引擎 + 细粒度度量监控 (TypeScript)

数据库经典 ARIES 协议的完整三阶段实现，配套高精度时序度量采集、统计聚合与导出。
纯 TypeScript（可擦除语法子集），零依赖，直接由 Node.js ≥ 23.6 内置 type-stripping 运行，
测试使用 Node.js 内置测试运行器（`node:test`）。

## 运行

```bash
npm run sample   # 构造崩溃 WAL 并执行三阶段恢复，打印页面结果与度量导出
npm test         # 72 个测试：恢复正确性 + 度量边界 + 并发安全 + 无泄漏断言
```

## 架构

```
src/
  errors.ts              统一错误码目录（E_WAL_*/E_RECO_*/E_METRIC_*/...）与 EngineError
  metrics/
    types.ts             标签、快照类型、级联键规范化
    primitives.ts        Counter / Gauge / Histogram（le 桶 + 精确百分位）
    timer.ts             performance.now() 高精度计时器（异常安全）
    registry.ts          注册表：级联键去重、类型冲突检测、错误码计数
    exporter.ts          JSON 导出、Prometheus 文本导出、跨批快照聚合
  wal/
    record.ts            日志记录类型（UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT）+ CRC32
    wal.ts               帧式 WAL（length+CRC32+payload），撕裂写检测，追加模式
  storage/
    disk.ts              定长页文件（pageLSN 头），读写延迟直方图
    bufferPool.ts        缓冲池：命中率计数、脏页 recLSN 追踪、静默性断言
  txn/
    transactionManager.ts 事务管理：更新/提交/模糊检查点（DPT+TT 快照）
  recovery/
    aries.ts             Analysis → Redo → Undo 三阶段恢复引擎
examples/
  scenario.ts            验收场景：5 事务（3 未提交 + 2 已提交）、检查点后崩溃
  run-sample.ts          端到端样例：构造崩溃 → 恢复 → 校验 → 度量导出
test/                    node:test 测试套件（72 个用例）
```

## ARIES 三阶段

- **Analysis**：定位最后一个 END_CHECKPOINT，以其 DPT/TT 快照为起点前向扫描至日志尾，
  重建崩溃瞬间的 DirtyPageTable（pageId → recLSN）与 TransactionTable（状态 + lastLSN），
  END 记录从事务表中移除事务。
- **Redo**：从 DPT 最小 recLSN 开始重复历史。仅当记录在 DPT 中且 `lsn >= recLSN`、
  且磁盘页 `pageLSN < lsn` 时才应用 afterImage，否则计入 `recovery.redo.skipped`。
- **Undo**：以败者事务（未 COMMITTED）的 lastLSN 构建最大 LSN 优先队列，逆序回滚：
  对 UPDATE 写 CLR（`undoNextLSN = 被补偿记录的 prevLSN`）并恢复 beforeImage；
  对 CLR 沿 `undoNextLSN` 跳转（崩溃重启幂等）；链尾写 END 记录。

## 度量与导出

- 计数器/仪表/直方图（累积 le 桶 + 保留样本精确百分位 p50/p90/p95/p99）
- 每阶段延迟直方图：`recovery.{analysis,redo,undo}.duration_ms`
- 命中率：`buffer.hit` / `buffer.miss`，导出时派生 `buffer.hit_rate`
- 错误码：`errors.total{code=...}` 按错误码分标签计数
- 导出：`exportJSON`（含派生比率）、`exportPrometheus`（文本 expositions）、
  `aggregateSnapshots`（跨注册表/时间窗聚合，直方图按桶合并并插值估算百分位）

## 验收场景

T1、T2 在检查点后提交；T3、T4、T5 未提交时崩溃（WAL 共 14 条记录）。
恢复后：P1=T1b（T5 的未提交覆盖被 CLR 回滚）、P2/P4 为 T2 已提交值、
P3/P5 恢复为初始值；WAL 尾部追加 4 条 CLR 与 3 条 END；
二次执行为完全无操作（幂等重启）。
