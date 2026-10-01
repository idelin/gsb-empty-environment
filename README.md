# 反例缩减与有界验证（固定有限状态模型）

在**固定模型**下，对触发性质违反的场景（初始配置 + 动作轨迹 + 故障集合）
做贪心 1-最小化，并给出三值结论的有界显式状态验证器。纯 Python 3.11
标准库，测试使用 `unittest`。

## 语义约定

- **可重放**：场景在模型上确定性重放；只有重放结果为 `violation` 的场景
  才是反例。动作前置条件不满足时重放结果为 `invalid`——删除动作导致前置
  不满足的候选**不能接受**。
- **同一反例**：以错误签名（`error_of` 返回的字符串）标识。重放得到不同
  错误的候选不是同一反例，**不能接受**。
- **局部最小**：仅当不动点扫描完成（每个单点删除——动作/配置项/故障——
  都被测试且无法复现同一错误）才报告 *locally 1-minimal*，并显式声明
  **这不是全局最小**。
- **预算耗尽**：重放次数预算耗尽时停止，报告中明确 `NOT confirmed`，
  不宣称任何（局部或全局）最小性。
- **验证结论**：`safe`（穷举完成且无违反）、`violated`（找到可重放反例）、
  `undecided`（状态/深度预算截断搜索——未搜索完就保持未决，绝不报 safe）。

## 文件结构

- `model.py` — 模型定义与确定性重放引擎（`violation` / `clean` / `invalid`）
- `shrinker.py` — 场景缩减器（`Scenario`、`Shrinker`、`ShrinkReport`）
- `verifier.py` — 有界 BFS 验证器（`safe` / `violated` / `undecided`）
- `example_model.py` — 示例模型：主备寄存器 + 故障注入
  （`partition`、`double_apply` 等；错误 `ERR_STALE_READ` / `ERR_REPLICA_AHEAD`）
- `demo.py` — 运行样例
- `tests/` — `unittest` 测试（17 个）

## 运行

```sh
python3.11 demo.py                          # 运行样例
python3.11 -m unittest discover -s tests -v # 运行测试
```

## 运行样例（`python3.11 demo.py` 实际输出）

```
====================================================================
1. 有界验证：故障全集内搜索性质违反
====================================================================
verdict         : violated
states explored : 67
error signature : ERR_STALE_READ
counterexample (replayable):
  config : {}
  actions: [start, write, replicate, read]
  faults : {double_apply}
====================================================================
2. 独立重放验证器给出的反例（必须可重放）
====================================================================
replay status : violation
replay error  : ERR_STALE_READ  (与验证器报告一致: True)
====================================================================
3. 无故障子模型：穷举完成，判定 safe
====================================================================
verdict         : safe
states explored : 56
====================================================================
4. 状态预算过小：搜索未完成，保持 undecided
====================================================================
verdict         : undecided
states explored : 3
search incomplete: state/depth budget exhausted; no conclusion about safety is drawn
====================================================================
5. 缩减一个臃肿的反例场景（初始配置 + 动作轨迹 + 故障集合）
====================================================================
原始场景:
  config : {'cache_warm': True, 'debug': True, 'primary_up': True}
  actions: [noop, write, noop, replicate, noop, read, noop, noop]
  faults : {logging, metrics, partition}
原始重放      : status=violation, error=ERR_STALE_READ, 在第 6 步触发

缩减过程:
  - drop action[0] 'noop'
  - drop action[1] 'noop'
  - drop action[2] 'noop'
  - drop action[3] 'noop'
  - drop action[3] 'noop'
  - reset config['cache_warm'] to model default
  - reset config['debug'] to model default
  - drop fault 'logging'
  - drop fault 'metrics'

缩减报告:
error signature : ERR_STALE_READ
replays used    : 20/1000
minimality      : locally 1-minimal -- every single deletion (action/config/fault) was tested and fails to reproduce 'ERR_STALE_READ'. This is NOT a claim of global minimality.
scenario:
  config : {'primary_up': True}
  actions: [write, replicate, read]
  faults : {partition}
====================================================================
6. 独立重放局部最小场景，确认触发同一性质违反
====================================================================
replay status : violation
replay error  : ERR_STALE_READ  (与目标错误一致: True)
====================================================================
7. 重放预算耗尽：不得宣称任何最小性
====================================================================
error signature : ERR_STALE_READ
replays used    : 4/4
minimality      : NOT confirmed -- replay budget exhausted before the fixpoint pass completed; the scenario may still be shrinkable. No minimality (local or global) is claimed.
scenario:
  config : {'cache_warm': True, 'debug': True, 'primary_up': True}
  actions: [write, replicate, noop, read, noop, noop]
  faults : {logging, metrics, partition}
```

## 测试结果（`python3.11 -m unittest discover -s tests -v`）

17 个测试全部通过，覆盖：重放确定性与 `invalid` 判定、前置条件破坏的
删除被拒绝、不同错误不视为同一反例、局部最小的暴力校验、预算耗尽不宣称
最小性、验证器 `safe` / `violated`（反例可重放）/ `undecided` 三值结论。

```
test_clean_trace_replays_clean ... ok
test_precondition_failure_is_invalid_not_violation ... ok
test_replay_is_deterministic ... ok
test_unknown_action_is_invalid ... ok
test_bloated_scenario_shrinks_to_local_minimum ... ok
test_budget_exhaustion_claims_no_minimality ... ok
test_budget_of_one_only_confirms_the_replay ... ok
test_deletion_breaking_precondition_is_rejected ... ok
test_different_error_is_not_the_same_counterexample ... ok
test_never_claims_global_minimality ... ok
test_refuses_non_violating_input ... ok
test_safe_when_fault_universe_empty ... ok
test_shrinking_verifier_counterexample_preserves_error ... ok
test_undecided_when_depth_budget_too_small ... ok
test_undecided_when_state_budget_too_small ... ok
test_violated_with_replayable_counterexample ... ok
test_violation_at_initial_state_has_empty_trace ... ok

----------------------------------------------------------------------
Ran 17 tests in 0.002s

OK
```
