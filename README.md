# CDC Pipeline：原子提交 + 幂等输出 + 崩溃恢复

Python 3.11，仅标准库（`sqlite3` / `json` / `hashlib` / `unittest`），无消息平台。
变更来自本地夹具 `fixtures/changes.json`，每条含事务 ID、表、主键、前后值、提交序号。

## 核心保证

1. **同一逻辑提交**：输入偏移推进与输出生成写入同一个 SQLite 事务
   （`StateStore.commit_batch`）。崩溃只会两者都落盘或都回滚，
   偏移绝不可能先于输出承诺推进。
2. **输出端幂等 ID**：`sha256(tx_id|table|pk|commit_seq)` 确定性生成，
   outbox 与下游 sink 均以它为主键去重。
3. **崩溃恢复**：未确认（`pending`）输出重启后重发；sink 按幂等 ID
   `INSERT OR IGNORE`，重发不产生重复业务效果；已确认输入由持久化
   偏移保护，不会丢失也不会重复入队。
4. **失败批次可重试**：批次先整体校验再提交；任何失败整体回滚，
   偏移不动，修正后可原样重试。

## 结构

- `cdc_pipeline/models.py` — `ChangeEvent`（校验、幂等 ID、输出载荷）、`OutboxRecord`
- `cdc_pipeline/store.py` — 状态库：`offsets` + `outbox` 同库同事务提交
- `cdc_pipeline/pipeline.py` — 夹具加载、批次校验、原子 ingest
- `cdc_pipeline/dispatcher.py` — 发送 pending 输出 → sink 确认 → 标记 acked
- `cdc_pipeline/sink.py` — 下游幂等接收器（独立 SQLite + JSONL 审计文件）
- `main.py` — 四阶段演示：正常投递 → 发送后崩溃 → 重启恢复 → 失败批次重试
- `tests/test_pipeline.py` — 10 个 unittest 用例

## 运行样例

```
$ python3 main.py
loaded 6 change events from fixtures/changes.json
[phase 1] offset=3 delivered+acked=3 sink_effects=3
[phase 2] offset=6 crash during delivery: SIMULATED CRASH
[phase 2] outbox at crash: {'acked': 3, 'pending': 3} sink_effects=4
[phase 3] redelivered unacked=3 (deduped downstream)
[phase 3] final: offset=6 outbox={'acked': 6} sink_effects=6
[phase 4] batch rejected: invalid event tx-1008: no before/after values; offset still 6
[phase 4] retried fixed batch: offset=8 acked=2 sink_effects=8
OK: 8 unique business effects, no loss, no duplicates
```

阶段 2 在"已发送未确认"处崩溃（sink 已应用第 4 条但 outbox 未标记）；
阶段 3 重启重发 3 条 pending，其中 1 条被下游幂等去重，最终恰好 6 个业务效果。

## 测试结果

```
$ python3 -m unittest discover -s tests -v
test_crash_after_send_before_ack_recovers_without_duplicates ... ok
test_delivery_acks_and_sink_applies_once ... ok
test_duplicate_event_in_batch_deduped_by_idempotency_id ... ok
test_failed_batch_rolls_back_and_stays_retryable ... ok
test_fixture_loads_sorted_with_all_fields ... ok
test_ingest_commits_offset_and_outputs_atomically ... ok
test_offset_never_advances_past_committed_outputs ... ok
test_reingest_of_confirmed_input_is_skipped ... ok
test_state_survives_reopen ... ok
test_unacked_output_resent_but_deduped_downstream ... ok

Ran 10 tests in 0.068s
OK
```
