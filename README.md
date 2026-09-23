# 公共子计划共享：物化 / 重算 / 溢写决策

纯 Python 标准库实现（不调用任何 SQL 引擎），演示多个查询共享公共子计划时，
如何按**读取次数、写入代价、内存代价**在「重算 / 内存物化 / 溢写物化」之间做
代价决策，并保证共享的正确性与物化的原子性。

## 语义约定（`subplan/relation.py`）

- **多重集合（bag）**：表是行的多重集合，重复行保留；Filter/Project 不改变
  基数，Join 按 bag 语义乘法展开（左 2 份重复 × 右 2 份重复 = 4 条结果）。
- **NULL**：用 `None` 表示；比较遵循三值逻辑（与 NULL 比较得 UNKNOWN），
  Filter 只保留谓词为 `True` 的行；等值连接中 NULL 不等于任何值（含 NULL）。

## 决策模型（`subplan/costing.py`）

对公共子计划比较三种方案总代价，取最低：

| 方案 | 总代价 |
| --- | --- |
| 重算 | `consumers × 计算代价` |
| 内存物化 | `计算 + 内存写入 + consumers × 内存读取 + 内存占用`（要求占用 ≤ 预算） |
| 溢写物化 | `计算 + 落盘写入 + consumers × 磁盘读取`（超预算时的兜底） |

规则与正确性保证：

- **一次使用不无故物化**：仅 1 个消费者时直接重算。
- **不同过滤不错误共享**：`largest_common_subplan` 找到的是过滤**之前**的
  公共子计划，物化的永远是未过滤结果，各消费者在共享结果上各自过滤。
- **物化原子性**：`MaterializedStore.put` 先完整写入私有缓冲/临时文件，
  成功后才登记；中途失败清理临时文件，读者永远看不到半张表。
- 每次决策都输出人类可读的依据（`decision.rationale()`）。

## 运行

```bash
python3 examples/demo.py          # 运行样例
python3 -m unittest discover -s tests -v   # 运行测试
```

## 运行样例（实际输出）

```text
两个查询的公共子计划（过滤之前的部分）： join
================================================================
场景 1：内存充足，2 个消费者 → 内存物化
================================================================
公共子计划估计 4 行 / 约 320 字节，消费者 2 个，单次计算代价 32.0。
  重算      ：2 × 32.0 = 64.0
  内存物化  ：32.0 计算 + 6.4 写入 + 2 × 3.2 读取 + 1.6 内存占用 = 46.4（占用 320B ≤ 预算 1048576B）
  溢写物化  ：32.0 计算 + 64.0 写入 + 2 × 16.0 读取 = 128.0
→ 选择内存物化：总代价最低（46.4）。
Q1（salary>60000）: ['alice', 'carol', 'carol']
Q2（dept='Engineering'）: ['alice', 'bob']
物化的共享结果（未过滤，含重复行 carol×2）: ['alice', 'bob', 'carol', 'carol', 'erin']
================================================================
场景 2：内存预算极小、8 个消费者 → 溢写物化
================================================================
公共子计划估计 4 行 / 约 320 字节，消费者 8 个，单次计算代价 32.0。
  重算      ：8 × 32.0 = 256.0
  内存物化  ：不可行（占用 320B > 预算 64B，需溢写）
  溢写物化  ：32.0 计算 + 64.0 写入 + 8 × 16.0 读取 = 224.0
→ 选择溢写物化：总代价最低（224.0）。
溢写后读回 Q1 结果: ['alice', 'carol', 'carol']
================================================================
场景 3：仅 1 个消费者 → 不无故物化，直接重算
================================================================
公共子计划估计 4 行 / 约 320 字节，消费者 1 个，单次计算代价 32.0。
  重算      ：1 × 32.0 = 32.0
  内存物化  ：32.0 计算 + 6.4 写入 + 1 × 3.2 读取 + 1.6 内存占用 = 43.2（占用 320B ≤ 预算 1048576B）
  溢写物化  ：32.0 计算 + 64.0 写入 + 1 × 16.0 读取 = 112.0
→ 选择重算：仅 1 次读取，物化没有复用收益，不无故物化。
物化存储为空: True
```

## 测试结果（实际输出）

```text
test_expensive_write_falls_back_to_recompute (test_decision.TestDecision) ... ok
test_many_consumers_materialize_in_memory (test_decision.TestDecision) ... ok
test_over_budget_spills_to_disk (test_decision.TestDecision) ... ok
test_rationale_lists_all_costs (test_decision.TestDecision) ... ok
test_single_consumer_never_materializes (test_decision.TestDecision) ... ok
test_filter_preserves_duplicates (test_semantics.TestBagSemantics) ... ok
test_join_multiplies_duplicates (test_semantics.TestBagSemantics) ... ok
test_project_preserves_duplicates (test_semantics.TestBagSemantics) ... ok
test_filter_drops_unknown (test_semantics.TestNullSemantics) ... ok
test_join_null_key_never_matches (test_semantics.TestNullSemantics) ... ok
test_sql_compare_with_null_is_unknown (test_semantics.TestNullSemantics) ... ok
test_memory_failure_is_atomic (test_sharing.TestAtomicMaterialization) ... ok
test_spill_failure_is_atomic (test_sharing.TestAtomicMaterialization) ... ok
test_common_subplan_is_below_filters (test_sharing.TestSharingCorrectness) ... ok
test_single_consumer_recomputes_and_does_not_store (test_sharing.TestSharingCorrectness) ... ok
test_two_consumers_different_filters (test_sharing.TestSharingCorrectness) ... ok
test_spill_results_equal_recompute (test_sharing.TestSpillRoundTrip) ... ok

----------------------------------------------------------------------
Ran 17 tests in 0.011s

OK
```

## 目录结构

- `subplan/relation.py` — 关系表与 NULL / bag 语义、三值逻辑比较函数
- `subplan/plan.py` — Scan/Filter/Project/Join 算子与公共子计划识别
- `subplan/costing.py` — 代价模型与物化/重算/溢写决策（含依据输出）
- `subplan/store.py` — 物化存储（内存 / 溢写临时文件，原子提交）
- `subplan/engine.py` — 执行引擎：决策 + 向各消费者分发迭代器
- `tests/` — 17 个 unittest（语义、决策、共享正确性、原子性）
- `examples/demo.py` — 三个场景的端到端样例
