# 事务性输入/输出管道（Transactional Outbox）

Python 3.11 + 标准库（`sqlite3`、`unittest`）实现。输入是**本地夹具**（JSON 文件，
含事务 ID、表、主键、前后值、提交序号），不接任何消息平台；输出端是一个本地持久接收器，
支持输出幂等 ID。崩溃恢复保证：**不丢已确认输入、不重复业务效果**。

## 核心不变量

1. **偏移与输出在同一逻辑提交记录中**：每个夹具批次（一个 `commit_seq`）在一个
   `BEGIN IMMEDIATE` 事务里同时写入五样东西——
   - `commit_record`：逻辑提交记录（提交序号、事务 ID、变更/输出条数、提交时刻）；
   - `applied_change`：已确认输入（按 `txn_id:table:key` 去重）；
   - `projection`：业务效果（after 落盘，after=null 即删除）；
   - `outbox`：输出承诺（`pending`，携带输出端幂等 ID）；
   - `meta.high_water_seq`：输入偏移（高水位提交序号）。
   任一写入失败整体回滚，所以**偏移不可能先于输出承诺推进**。
2. **输出端幂等**：每个输出生成确定性 ID
   `out-<seq>-sha256("seq=<seq>|uid=<txn>:<table>:<key>")[:16]`；
   接收器以该 ID 做持久去重（JSON 日志，临时文件 + `os.replace` + `fsync` 原子落盘）。
3. **至少一次发送 + 去重**：`outbox` 行只有在发送成功后才批量置为 `acked`。
   发送后未确认（崩溃/ACK 丢失）可以重发，接收器按幂等 ID 去重，业务效果只生效一次；
   若同一幂等 ID 的负载不一致，接收器拒绝去重并报错。
4. **失败批次可重试**：批次按提交序号顺序发送；遇到失败立即停止，已成功的先行确认，
   失败行及其后各行保持 `pending` 并记录尝试次数与错误，下次从同一行重试。
5. **崩溃恢复**：重启后重放夹具，高水位以下的批次直接跳过（不会重复应用），
   缺序号（空洞）报 `GapDetectedError`；`pending` 的输出继续发送。

持久化使用 SQLite WAL 模式与 `synchronous=FULL`。

## 目录结构

```
outbox/
  model.py     Change/Output 模型、夹具加载、幂等 ID 生成
  store.py     SQLite 存储：同一事务提交偏移+投影+outbox+提交记录
  sink.py      本地输出接收器：持久日志、幂等去重、失败/丢ACK注入
  processor.py 摄取（偏移推进）与发送（确认/重试）编排
  errors.py    异常层次
sample/
  fixtures/accounts.json  本地夹具：4 个提交（插入/更新/插入/删除）
  run_demo.py             端到端演示（子进程模拟真实进程崩溃）
tests/test_pipeline.py    23 个 unittest 用例
```

## 运行样例

```bash
python3.11 sample/run_demo.py demo
```

演示分 6 个阶段，每阶段是独立 Python 子进程（崩溃是真实进程退出，状态全部落盘恢复）：

| 阶段 | 操作 | 结果 |
| --- | --- | --- |
| 0 | 清空状态 | 空库 |
| 1 | 提交 seq 1、2 后立即崩溃（exit 99） | 高水位=2，投影已落盘，2 个输出 pending，尚未发送 |
| 2 | 重启并重放整个夹具 | seq 1、2 作为陈旧批次被跳过，仅提交 seq 3、4 |
| 3 | 重启发送，seq 3 注入“发送失败” | seq 1、2 确认；seq 3、4 保持 pending（批次可重试） |
| 4 | 重启重试，seq 3 注入“ACK 丢失” | 接收器已持久 seq 3（unique=3），但 outbox 仍 pending |
| 5 | 重启再发 | seq 3 重发被幂等去重，seq 4 发送，outbox 清零 |

最终：`high_water=4`、`pending=0`、接收器 `unique_delivered=4`、
`duplicate_deliveries=1`，业务投影仅剩 acct-2（acct-1 被 seq 4 删除）。
程序末尾自动校验全部不变量并打印 `RESULT: PASS`。

也可分步手动运行：

```bash
python3.11 sample/run_demo.py reset
python3.11 sample/run_demo.py ingest --crash-after 2   # 退出码 99
python3.11 sample/run_demo.py ingest                   # 恢复，跳过已确认批次
python3.11 sample/run_demo.py send
python3.11 sample/run_demo.py status
```

## 运行测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：偏移/输出同事务提交、planner 失败整体回滚、偏移不超前、陈旧批次拒绝、
空洞中止、批内重复、删除墓碑、多变更事务原子性、接收器跨重开去重、提交后崩溃恢复、
发送失败留重试、ACK 丢失重发去重、失败批次跨进程恢复、夹具端到端等 23 个用例。

## 需求映射

- 输入偏移与生成输出同一逻辑提交 → `Store.commit_batch` 单事务（`store.py`）
- 输出端幂等 ID → `model.output_id` + `LocalSink` 以 ID 持久去重
- 崩溃恢复不丢已确认输入/不重复业务效果 → 高水位跳过陈旧批次 + outbox 至少一次 + 接收器去重
- 变更来自本地夹具（txn/表/主键/前后值/提交序号）→ `sample/fixtures/accounts.json`，无消息平台
- 输出发送后未确认可重发但去重 → `Processor.send_pending` + `lose_ack_once`
- 偏移不得先于输出承诺推进 → 同事务提交，planner 期间校验高水位仍为旧值（有测试）
- 失败批次保持可重试 → 失败行保持 `pending`、记录 attempts/last_error，按序重试
