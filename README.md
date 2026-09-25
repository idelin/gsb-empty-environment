# Sharded Embedding + Sparse Optimizer (pure stdlib, Python 3.11)

从空仓库实现的行分片嵌入表、重复索引梯度合并、微批累积与稀疏动量优化器。
不依赖任何自动微分或张量库，小规模 CPU 张量用 `list[float]` 自行实现
（`minitensor.py`），测试仅使用标准库 `unittest`。

## 文件

- `minitensor.py` — 纯 Python 向量工具（零向量、原地累加、近似比较）。
- `embedding.py` — `ShardedEmbedding`（行分片、查表、行迁移）、
  `GradientAccumulator`（跨微批合并重复索引）、`SparseMomentumSGD`
  （原子多分片提交）、JSON checkpoint 保存/恢复。
- `reference.py` — 独立编写的稠密小表参考实现，用于数值交叉核验。
- `test_embedding.py` — 全部可运行测试。

## 明确语义（实现与参考一致）

- **权重衰减**：只对当前步骤合并梯度触达的行施加（稀疏语义）；未触达行
  不衰减、不动量更新。由 `test_untouched_rows_get_no_weight_decay` 锁定。
- **冻结行**：查表正常；优化器丢弃其梯度，权重/动量永不变，也不受衰减。
- **步数**：按分片计数，每个优化步骤中只要该分片更新了至少一行就恰好
  推进一次；空步骤不推进步数，但仍记录批次 ID。
- **原子性**：步骤开始前快照所有将触达的分片；任一分片提交失败即整体
  回滚（权重+动量+步数），批次 ID 不记录，可安全重试。
- **幂等恢复**：已应用批次 ID 持久化在 checkpoint 中，重放同一批次 ID
  直接跳过。
- **分片迁移**：迁移行使 `layout_version` 递增；梯度携带产生时的版本，
  版本过期即抛 `StaleGradientError`，不会写入错误的分片。

更新规则（每个被触达行、每个步骤）：

```
g' = g + weight_decay * w
m  = beta * m + g'
w  = w - lr * m
```

## 运行方式

```
python3.11 -m unittest -v
```

## 实际运行结果（Python 3.11.15, macOS arm64）

```
test_second_shard_failure_rolls_back_first (test_embedding.AtomicityTest.test_second_shard_failure_rolls_back_first) ... ok
test_checkpoint_roundtrip_and_idempotent_replay (test_embedding.CheckpointRecoveryTest.test_checkpoint_roundtrip_and_idempotent_replay) ... ok
test_sharded_matches_dense_over_many_steps (test_embedding.DenseReferenceEquivalenceTest.test_sharded_matches_dense_over_many_steps) ... ok
test_untouched_rows_get_no_weight_decay (test_embedding.DenseReferenceEquivalenceTest.test_untouched_rows_get_no_weight_decay) ... ok
test_empty_indices_step (test_embedding.FrozenAndEmptyTest.test_empty_indices_step) ... ok
test_frozen_rows_never_update (test_embedding.FrozenAndEmptyTest.test_frozen_rows_never_update) ... ok
test_duplicate_indices_merged_row2_doubled (test_embedding.LookupAndMergeTest.test_duplicate_indices_merged_row2_doubled) ... ok
test_lookup_returns_copies (test_embedding.LookupAndMergeTest.test_lookup_returns_copies) ... ok
test_two_microbatches_merge_and_step_advances_once (test_embedding.MicroBatchAccumulationTest.test_two_microbatches_merge_and_step_advances_once) ... ok
test_migration_carries_momentum (test_embedding.MigrationTest.test_migration_carries_momentum) ... ok
test_stale_gradient_rejected_after_migration (test_embedding.MigrationTest.test_stale_gradient_rejected_after_migration) ... ok

----------------------------------------------------------------------
Ran 11 tests in 0.006s

OK
```

## 需求到测试的映射

- 索引 `[2,2,5]` 行 2 贡献翻倍 → `test_duplicate_indices_merged_row2_doubled`
- 同一步两微批重复索引合并、步数只推进一次 →
  `test_two_microbatches_merge_and_step_advances_once`
- 迁移期间旧路由梯度带版本、不可写入错误分片 →
  `test_stale_gradient_rejected_after_migration`
- 第二分片失败回滚第一分片 → `test_second_shard_failure_rolls_back_first`
- 稠密小表参考核验参数及动量 → `test_sharded_matches_dense_over_many_steps`
- 冻结行 → `test_frozen_rows_never_update`
- 空索引 → `test_empty_indices_step`
- checkpoint 恢复与按批次 ID 幂等 →
  `test_checkpoint_roundtrip_and_idempotent_replay`
