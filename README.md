# 行分片稀疏嵌入表与原子稀疏优化器

纯 Python 3.11 标准库实现，不依赖自动微分或任何张量库；小规模 CPU 张量以
`list[float]` 自行实现。测试使用 `unittest`，并内置独立的稠密参考实现做数值核验。

## 文件

- `sparse_embedding.py` — 实现：`ShardedEmbedding`（行分片 + 路由 + 迁移）、
  `GradientAccumulator`（微批梯度累积、重复索引合并）、`SparseMomentumSGD`
  （稀疏动量 SGD、跨分片原子提交、checkpoint 恢复）。
- `test_sparse_embedding.py` — 11 个可运行测试 + 稠密参考实现 `DenseReference`。

## 关键语义（明确且一致）

- **更新规则**（仅对被触达且未冻结的行）：
  `g_eff = grad + weight_decay * param`；`mom = beta * mom + g_eff`；
  `param -= lr * mom`。
- **权重衰减**：只对当前步合并梯度触达的、未冻结的行施加；**未触达行不衰减**，
  冻结行不衰减也不更新（稀疏语义，与稠密参考一致）。
- **重复索引**：同一微批或跨微批的重复索引按出现次数累加（`[2,2,5]` 中行 2 翻倍）。
- **微批累积**：多个微批先入 `GradientAccumulator` 合并，一次 `step()` 只推进一次步数。
- **原子性**：一次 `step` 先计算全量更新计划，再逐分片应用；任一分片失败即回滚所有
  已应用分片的参数与动量，步数与批次 ID 不前进——参数、动量、步数作为一个整体原子提交。
- **幂等恢复**：`step(..., batch_id=...)` 若等于最后已提交批次 ID 则为空操作；
  `checkpoint()/restore()` 保存并恢复参数、动量、路由、路由版本、步数与批次 ID，
  恢复后重放同一批次安全。
- **分片迁移**：每行有路由版本，迁移时 +1；前向查找返回版本并随梯度携带。
  优化器在写任何分片前校验版本，旧路由梯度（版本过期）抛 `StaleGradientError`，
  不会写入错误分片。
- **空梯度 / 全冻结**：空操作，不推进步数、不消耗批次 ID。

## 运行

```
python3.11 -m unittest -v
```

## 实际运行结果（Python 3.11.15，2026-09-26）

```
test_checkpoint_recovery_idempotent ... ok
test_dense_reference_params_and_momentum ... ok
test_duplicate_indices_double_contribution ... ok
test_empty_indices_noop ... ok
test_frozen_rows_not_updated ... ok
test_hand_computed_momentum_two_steps ... ok
test_migration_moves_state_and_bumps_version ... ok
test_second_shard_failure_rolls_back_first ... ok
test_stale_gradient_rejected_during_migration ... ok
test_two_microbatches_single_step ... ok
test_weight_decay_only_touched_rows ... ok

----------------------------------------------------------------------
Ran 11 tests in 0.001s

OK
```
