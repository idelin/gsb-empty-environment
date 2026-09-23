# Shard Transaction Assembler

跨表分片事务组装器：同一业务事务的变更按表分片到达，清单（manifest）齐备后
整体原子发布；超时仅标记未决（PENDING_TIMEOUT），绝不部分提交。变更来自本地
JSON 夹具，不依赖任何消息平台。仅使用 Python 3.11 标准库。

## 规则

- 每个事务有清单：所需表集合 + 截止时间，清单摘要是表集合的 SHA-256 摘要。
- 每个变更分片携带：事务 ID、表、主键、前后值、提交序号、清单摘要。
- 清单要求的表全部到达 → 按提交序号排序后原子发布。
- 清单有 A/B 但只到 A → 不发布。
- 同一表的重复分片保留首条，不计入其他缺失表。
- 分片摘要与清单摘要冲突 → 整事务拒绝（REJECTED）。
- 超时未齐备 → 标记 PENDING_TIMEOUT，缓冲保留但不发布。

## 文件

- `txn_assembler.py` — 核心模块（`TransactionAssembler`、`Manifest`、`ChangeEvent`）
- `fixtures/txn_00*.json` — 本地夹具（齐备 / 缺 B / 重复分片 / 摘要冲突）
- `run_demo.py` — 演示脚本：`python3 run_demo.py`
- `test_txn_assembler.py` — 单元测试：`python3 -m unittest -v test_txn_assembler.py`

## 运行样例

```
$ python3 run_demo.py
== Receiving shards ==
recv txn-001 table=A pk=a-1 seq=11 -> COLLECTING
recv txn-001 table=B pk=b-1 seq=12 -> PUBLISHED
recv txn-002 table=A pk=a-2 seq=21 -> COLLECTING
recv txn-003 table=A pk=a-3 seq=31 -> COLLECTING
recv txn-003 table=A pk=a-3 seq=31 -> COLLECTING
recv txn-004 table=A pk=a-4 seq=41 -> COLLECTING
recv txn-004 table=B pk=b-4 seq=42 -> REJECTED

== Timeout sweep at t=2500.0 ==
timeout: txn-002 marked PENDING_TIMEOUT (no partial commit)
timeout: txn-003 marked PENDING_TIMEOUT (no partial commit)

== Final transaction states ==
txn-001: PUBLISHED
txn-002: PENDING_TIMEOUT
txn-003: PENDING_TIMEOUT
txn-004: REJECTED (DIGEST_CONFLICT)

== Published transactions (commit_seq order) ==
publish txn-001 table=A pk=a-1 seq=11 {'qty': 3} -> {'qty': 5}
publish txn-001 table=B pk=b-1 seq=12 None -> {'note': 'created'}

== Downstream store ==
{
  "A": {
    "a-1": {
      "qty": 5
    }
  },
  "B": {
    "b-1": {
      "note": "created"
    }
  }
}
```

## 测试结果

```
$ python3 -m unittest -v test_txn_assembler.py
test_duplicate_shard_does_not_satisfy_missing_table ... ok
test_incomplete_manifest_not_published ... ok
test_publish_when_manifest_complete ... ok
test_published_in_commit_seq_order ... ok
test_conflicting_digest_rejects_transaction ... ok
test_rejected_transaction_ignores_late_shards ... ok
test_table_outside_manifest_rejected ... ok
test_apply_to_store_upsert_and_delete ... ok
test_no_timeout_before_deadline ... ok
test_published_transaction_not_affected_by_timeout ... ok
test_timed_out_transaction_never_publishes ... ok
test_timeout_marks_pending_without_partial_commit ... ok

----------------------------------------------------------------------
Ran 12 tests in 0.000s

OK
```
