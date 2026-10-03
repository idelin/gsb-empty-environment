# ARIES 崩溃恢复引擎 + 多节点并发仲裁器（Go，仅标准库）

实现经典 ARIES 崩溃恢复协议的三阶段扫描，以及一个基于 wound-wait
策略的多执行体锁仲裁器。仅使用 Go 标准库。

## 结构

- `wal/` — 预写日志：记录编码（UPDATE/CLR/COMMIT/ABORT/END/CHECKPOINT），
  追加写（LSN = 文件偏移）、顺序读、随机读；容忍崩溃留下的撕裂尾部。
  文件头部 8 字节魔数保证 LSN 0 永不有效，`PrevLSN == 0` 无歧义。
- `store/` — 页式存储 + 易失缓冲池；`Crash()` 模拟断电；刷页前强制
  执行 WAL 规则（pageLSN 必须先于日志落盘）。
- `recovery/` — ARIES 三阶段：
  - **Analysis**：从最近检查点扫描，重建 TransactionTable 与
    DirtyPageTable，得出 redo 起点（最小 recLSN）。
  - **Redo**：历史重复，按 recLSN/pageLSN 精确跳过已持久化的记录。
  - **Undo**：以最大 LSN 堆反向扫描败者事务，逐条写 CLR
    （含 undoNextLSN），遇 CLR 沿 undoNext 跳转绝不重复回滚；
    支持 undo 途中再次崩溃后续跑。
  - `Runtime` 辅助器用于生成事务负载与模糊检查点。
- `arbiter/` — 多节点锁仲裁器：S/X 锁、FIFO 等待队列、锁升级；
  wound-wait 策略（年长者击伤年幼者，年幼者等待），等待图边只会
  从年轻指向年长，结构上无死锁；所有授予决策在单一互斥锁下原子
  完成，保证线性一致；被击伤事务的锁在其回滚（`Finish`）时才释放，
  维护 2PL 不变量。
- `cmd/aries-demo/` — 运行样例。

## 运行

```sh
go test -race ./...      # 全部测试（含竞态检测）
go run ./cmd/aries-demo  # 崩溃恢复 + 并发仲裁演示
```

## 验收场景

`recovery.TestAcceptanceCrashRecovery` 构造：T1/T2 已提交（T2 未写
END）、T3/T4/T5 未提交，检查点之后崩溃；含未提交数据的页 1/2 被
偷刷到磁盘，页 3/4/5 仅存在于丢失的缓冲池中。恢复后断言磁盘态
`P1=100, P2=200, P3=300, P4=400, P5=0`，并验证 CLR/END 日志数量、
恢复幂等性、undo 途中崩溃的续跑、ABORT 事务回滚、prevLSN 链完整
回滚等边界。
