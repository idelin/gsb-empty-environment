# tsmonitor — 有限时序规则的三值在线监视器

Python 3.11，仅标准库 + unittest。不使用任何现成正则/自动机库：
每条规则的字母表与转移结构显式输入（前缀模式被物化为显式转移表
`dict[(state, symbol)] -> state`，见 `tsmonitor/rules.py`）。

## 模型

- **规则**（`RuleSpec`）：`prefix` 触发序列在事件时间 `t` 完成时开启义务，
  窗口为 `(t, t+window]`；`require` 为窗口内必须出现的符号（A后限时B），
  `forbid` 为窗口内禁止出现的符号（期间无C）。多条规则可共享同一前缀
  （各自独立的转移表在同一事件流上投影运行）。`consume_witness` 按规则
  定义"同一个 B 是否可满足多个 A"。
- **三值判定**：`TRUE / FALSE / PENDING` + `final` 标志。窗口未闭时的
  满足只是暂定（可被迟到 C 或撤回修订）；只有水位线越过窗口右端或
  终止（`terminate`）才封闭义务。所有发布过的判定版本都保留在义务的
  `history` 与监视器的全局 `log` 中。
- **乱序与撤回**：每次变更后对保留窗口内的事件重算；义务以
  `(规则名, 触发事件id)` 稳定标识，因此迟到事件与 `retract(id)` 都能
  修订已发布判定。重复事件按 `id` 去重，不产生新义务。
- **背压**：状态受 `lateness` 额度约束，保留界为
  `max(max_ts - lateness, watermark)`。无法保留的事件显式抛出
  `BackpressureError`，绝不把预算中止当作违反。
- **反例**：`counterexample()` 返回最短违反证据（事件数最少、窗口最早）。
  `Witness.verify()` 独立复核；`Witness.to_script()` 导出自包含脚本，
  可脱离本包独立执行（`python3 counterexample.py`，成立时输出
  `VIOLATION CONFIRMED` 并以 0 退出）。
- **检查点**：`checkpoint()` 序列化未决义务、撤回索引、判定日志等全部
  状态（JSON 可往返）；`Monitor.restore` 恢复后继续回放。

## 运行测试

```sh
python3.11 -m unittest discover -v
```

## 实际运行结果（2026-09-23，Python 3.11.15，macOS）

```
test_budget_abort_is_explicit_not_a_violation ... ok
test_batched_replay_with_checkpoints_matches_sorted_reference ... ok
test_checkpoint_preserves_pending_and_retraction_index ... ok
test_counterexample_is_shortest ... ok
test_pure_forbid_rule ... ok
test_terminate_closes_obligations ... ok
test_watermark_closes_missing_requirement_as_violation ... ok
test_multi_symbol_prefix_uses_explicit_transition_table ... ok
test_pending_while_window_open ... ok
test_duplicates_create_no_new_obligations ... ok
test_late_c_revises_tentative_satisfaction ... ok
test_retracting_require_event_reopens_obligation ... ok
test_same_b_satisfying_multiple_as_is_per_rule ... ok
test_witness_script_runs_independently ... ok

Ran 14 tests in 0.068s
OK
```

测试与需求场景的对应：

| 需求 | 测试 |
| --- | --- |
| A后未见B且窗口未闭 → 未决 | `test_pending_while_window_open` |
| 迟到C撤销暂定满足；撤回再修订 | `test_late_c_revises_tentative_satisfaction` |
| 重复事件不产生新义务 | `test_duplicates_create_no_new_obligations` |
| 同B是否满足多A按规则定义 | `test_same_b_satisfying_multiple_as_is_per_rule` |
| 终止/水位线封闭义务 | `test_terminate_closes_obligations`、`test_watermark_closes_missing_requirement_as_violation` |
| 最短违反证据、独立执行 | `test_counterexample_is_shortest`、`test_witness_script_runs_independently` |
| 预算中止显式背压、不算违反 | `test_budget_abort_is_explicit_not_a_violation` |
| checkpoint 保存未决义务与撤回索引 | `test_checkpoint_preserves_pending_and_retraction_index` |
| 分批回放与完整排序参考一致 | `test_batched_replay_with_checkpoints_matches_sorted_reference` |
