# 有限时序规则的三值在线监视器

Python 3.11+，仅标准库与 unittest，无正则/自动机库。

## 设计

- **显式字母表与转移结构**：`RuleTrie(alphabet)` + `add_rule(name, path, bound, forbid, consume)`，
  编译为共享前缀 trie（`transitions()` 给出显式转移表）。示例规则
  `ab`（A 后 10 秒内 B、期间无 C）与 `ad`（A 后 20 秒内 D）共享前缀节点 `("A",)`。
- **三值判定**：PENDING / SATISFIED（暂定） / VIOLATED。判定由保留事件按 `(ts, eid)`
  排序后确定性重放得出，因此乱序迟到事件与撤回（`retract(eid)`）会修订已发布判定；
  所有旧版本保存在 `Obligation.history` 与 `Monitor.verdict_log`。
- **封闭**：仅水位线越过 deadline 或 `close()` 终止能封闭义务；窗口未闭前保持 PENDING。
  越过 `deadline + lateness` 后判定封存为 final，不再修订。
- **背压**：事件超出迟到窗口（`ts <= watermark - lateness`）或保留预算（`max_events`）
  耗尽时抛 `BackpressureError`，不发布任何判定——预算中止绝不当违反。
- **去重与共享**：重复 `eid` 不产生新义务；同一 B 是否满足多个 A 由规则的
  `consume` 标志定义（False=共享，True=被最老未决义务消费）。
- **最短违反证据**：`Witness` 只含触发事件 + 首个致违事件（或仅触发事件 + 超时标记），
  `replay_witness(rule, witness)` 不依赖监视器状态，可独立重放验证。
- **Checkpoint**：`snapshot()` 输出 JSON 可序列化字典，含未决义务、撤回索引
  （`retraction_index`）、判定日志；`Monitor.restore(trie, snap)` 恢复后续跑。

## 文件

- `monitor.py` — 核心实现（Event / RuleTrie / Monitor / Witness / replay_witness）
- `test_monitor.py` — 12 个可运行测试

## 实际运行结果

```console
$ python3 -m unittest test_monitor -v
test_budget_abort_is_explicit_and_not_a_violation (test_monitor.BackpressureTest.test_budget_abort_is_explicit_and_not_a_violation) ... ok
test_too_late_event_is_backpressure_not_violation (test_monitor.BackpressureTest.test_too_late_event_is_backpressure_not_violation) ... ok
test_batched_replay_with_checkpoints_matches_sorted_reference (test_monitor.CheckpointTest.test_batched_replay_with_checkpoints_matches_sorted_reference) ... ok
test_alphabet_and_transitions_are_explicit (test_monitor.ExplicitStructureTest.test_alphabet_and_transitions_are_explicit) ... ok
test_a_without_b_while_window_open_is_pending (test_monitor.PendingTest.test_a_without_b_while_window_open_is_pending) ... ok
test_no_closure_without_watermark_or_termination (test_monitor.PendingTest.test_no_closure_without_watermark_or_termination) ... ok
test_duplicate_events_open_no_new_obligations (test_monitor.RevisionTest.test_duplicate_events_open_no_new_obligations) ... ok
test_late_c_revokes_tentative_satisfaction_and_retraction_restores (test_monitor.RevisionTest.test_late_c_revokes_tentative_satisfaction_and_retraction_restores) ... ok
test_same_b_consumed_by_oldest_a_when_rule_requires (test_monitor.RevisionTest.test_same_b_consumed_by_oldest_a_when_rule_requires) ... ok
test_same_b_satisfies_many_a_when_rule_allows_sharing (test_monitor.RevisionTest.test_same_b_satisfies_many_a_when_rule_allows_sharing) ... ok
test_deadline_witness_is_independently_executable (test_monitor.WitnessTest.test_deadline_witness_is_independently_executable) ... ok
test_shortest_forbidden_witness_is_independently_executable (test_monitor.WitnessTest.test_shortest_forbidden_witness_is_independently_executable) ... ok
  
  ----------------------------------------------------------------------
  Ran 12 tests in 0.002s
  
  OK
```
