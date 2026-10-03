# ARIES 崩溃恢复引擎（TypeScript）

数据库经典 **ARIES** 预写日志（WAL）分析与崩溃恢复 Redo/Undo 引擎，含异常注入与
崩溃恢复容错性验证。纯 TypeScript 实现，零第三方依赖，使用 Node.js 内置测试运行器
（`node:test`）验证。

## 功能

- **WAL**：追加式日志，LSN 单调递增、无空洞（内置互斥锁保证并发安全）；
  每条记录带 CRC32 校验，可识别断电造成的撕裂尾部（torn tail）并在重启时截断自愈。
- **Analysis 阶段**：从最近的模糊 Checkpoint（携带 TransactionTable 与
  DirtyPageTable 快照）出发正向扫描，精确重建崩溃瞬间的 TT 与 DPT。
- **Redo 阶段（历史重复）**：从 DPT 最小 recLSN 开始重放所有 UPDATE/CLR，
  依据 pageLSN 跳过已持久化的更新；撕裂页（torn page）检测后回退为全历史重放。
- **Undo 阶段（反向扫描）**：按最大 LSN 优先的全局逆序回滚所有 loser 事务，
  每个补偿动作写 **CLR**（含 undoNextLSN），恢复过程本身可崩溃重启且幂等。
- **异常注入**：确定性脚本 + 种子化概率注入，覆盖突发断电、磁盘满、网络丢包、
  恢复中途再次断电等恶劣工况；`assertNoPendingFaults` 保证注入的故障真正被触发。
- **无泄漏断言**：恢复后 WAL 缓冲全量落盘、缓冲池无脏页、事务表清空、
  每个磁盘页 pageLSN 与 WAL 最新记录一致、磁盘状态与独立模型重放一致。

## 运行要求

Node.js **>= 22.6**（使用 `--experimental-strip-types` 直接运行 TypeScript，
无需编译步骤）。`run.sh` 会自动在 PATH 与 `~/.nvm/versions/node` 中挑选合适版本。

```bash
npm test        # 或 ./run.sh test   —— 运行全部测试
npm run demo    # 或 ./run.sh demo   —— 运行崩溃恢复演示
```

## 目录结构

```
src/
  types.ts       日志记录 / 页 / 事务表类型定义
  crc32.ts       CRC32（日志记录与页的完整性校验）
  faults.ts      故障注入器 + DiskFull/PowerLoss/Crash/TornPage 错误
  wal.ts         预写日志：追加、刷盘、撕裂尾部截断、主 Checkpoint 记录
  storage.ts     模拟磁盘页存储（CRC 保护，可注入磁盘满 / 撕裂写）
  bufferpool.ts  缓冲池：steal/no-force，WAL-first 刷页，recLSN 跟踪
  txn.ts         事务管理器：begin/set/commit/abort/checkpoint（CLR 逻辑undo）
  recovery.ts    ARIES 恢复引擎：Analysis -> Redo -> Undo
  logshipper.ts  丢包信道 + 停等 ARQ 日志复制（自愈重传、去重）
  system.ts      系统引导 / 崩溃模拟门面
tests/
  acceptance.test.ts   验收场景：3 未提交 + 2 已提交事务，Checkpoint 后崩溃
  recovery.test.ts     恢复语义：幂等、undo 中途崩溃、END 丢失、abort 中断等
  faults.test.ts       异常注入：断电 / 磁盘满 / 撕裂页 / 网络丢包
  concurrency.test.ts  并发安全：LSN 完整性、事务链、并发崩溃恢复
examples/demo.ts       端到端演示（工作负载 -> 断电 -> 三阶段恢复）
run.sh                 自动选择 Node 版本运行测试 / 演示
```

## 验收场景（tests/acceptance.test.ts）

构造包含 **3 个未提交事务（T3,T4,T5）与 2 个已提交事务（T1,T2）** 的 WAL，
在中间执行模糊 Checkpoint 后崩溃：P1 被 steal 到磁盘（含未提交数据），
P2 的已提交更新仅在 WAL 中（no-force），最后一条 UPDATE 仍在内存缓冲区
（崩溃丢失）。恢复引擎执行三阶段扫描后断言：

- Analysis：TT = {T3,T4,T5}，DPT = {P1,P2,P3,P4}；
- Redo：重放丢失更新（含已落盘页的跳过逻辑）；
- Undo：恰好 5 条 CLR，回滚全部未提交修改；
- 最终磁盘：P1=`{a:1}`，P2=`{b:2,e:5}`，P3=P4=`{}`；
- 无泄漏：WAL 缓冲为空、无脏页、事务表为空、pageLSN 与 WAL 一致。

## 运行样例（`npm run demo`，节选）

```
*** POWER LOSS ***  (WAL buffer, buffer pool, txn table evaporate)
[4] Analysis:
  checkpoint LSN = 11
  transaction table: [{"txnId":3,...},{"txnId":4,...},{"txnId":5,...}]
  dirty page table:  [[1,2],[2,4],[3,10],[4,15]]
[5] Redo (repeat history): re-applied LSNs [4,10,12,15,17]
[6] Undo (losers [3,4,5]): wrote CLRs [19,20,22,23,25]
[7] Recovered durable state:
  P1 (pageLSN=25): {"a":1}
  P2 (pageLSN=12): {"b":2,"e":5}
  P3 (pageLSN=23): {}
  P4 (pageLSN=22): {}
Committed data persisted, uncommitted data rolled back. No leaks.
```

## 测试结果（`npm test`）

```
✔ acceptance: 3 uncommitted + 2 committed txns, crash after checkpoint
✔ concurrent txns: gap-free LSNs, intact chains, correct recovery
✔ concurrent commits and aborts racing a checkpoint stay consistent
✔ power loss mid-flush: torn WAL tail is detected, truncated and healed
✔ disk full on commit: commit fails safely, txn rolls back, system recovers
✔ disk full on page write: flush fails atomically and retries succeed
✔ torn page write: redo rebuilds the page from full log history
✔ network packet loss (scripted): retransmission heals replication exactly
✔ network packet loss (probabilistic 30%/20%): replica still converges
✔ recovery is idempotent: a second recovery is a no-op
✔ crash in the middle of undo: recovery restarts and completes
✔ committed txn whose END record was lost gets END rewritten (winner)
✔ txn that was mid-abort at crash resumes undo via undoNextLSN
✔ empty transaction (BEGIN only) is ended without any CLR
✔ normal abort writes CLRs and leaves nothing for recovery
✔ recovered state matches independent model replay of the durable log
✔ recovery without any checkpoint scans the whole log
✔ recovery of an empty log is a clean no-op
ℹ tests 18  ℹ pass 18  ℹ fail 0
```

## 故障模型与说明

- **断电**：WAL 缓冲区与缓冲池全部丢失；持久化介质上可能残留撕裂尾部
  （CRC 校验失败即截断）或撕裂页（Redo 时对该页回退为全历史重放）。
- **磁盘满**：WAL 刷盘失败 -> 提交安全失败（事务保持未提交，可回滚）；
  页写入失败是原子的（脏页保留在缓冲池，释放空间后重试自愈）。
- **网络丢包**：数据包与 ACK 均可丢失；停等 ARQ 重传 + 接收端按 LSN 去重，
  副本最终与源日志逐字节一致。
- 数据值约定：`null` 为保留哨兵（表示键不存在），不作为业务值存储。
- 页级原子写之外的撕裂页通过“全历史重放”恢复，等价于 double-write 的语义兜底。
