# Fair-Cycle Witness Checker

对给定有限状态模型检查指定动作的弱公平（weak fairness）/ 强公平
（strong fairness）约束，反例以"lasso"（有限 stem + 可无限重复的循环）
形式给出，且可重放验证。仅使用 Python 3.11 标准库；测试基于 `unittest`。

## 语义约定

对无限执行 `stem + cycle × ∞`：

- **弱公平违反**：动作在循环中**持续可用**（每个循环状态都 enabled）却从未执行。
- **强公平违反**：动作在循环中**间歇可用**（至少一个循环状态 enabled，
  循环重复即无限次可用）却从未执行。

两者严格区分，绝不混淆：持续可用才违反弱公平；间歇可用只涉及强公平。
每个见证用 `CycleStats` 记录动作在循环各状态上的可用情况
（`continuously_enabled` 与 `intermittently_enabled` 互斥）。

## 判定（Verdict）

- `VIOLATED`：找到可重放的反例 lasso（结论性）。
- `HOLDS`：可达状态空间已搜索完毕且无反例（结论性）。
- `PENDING`：搜索被状态上限截断（`max_states`），未搜索完，保持未决。

验证对象严格限于给定有限状态模型；不可达部分的循环不计入。

## 文件

- `fairness.py` — 核心库：`TransitionSystem`、`check_fairness`、
  `find_witness`、`replay`（重放并重新校验见证）。
- `main.py` — 四个样例模型的演示。
- `test_fairness.py` — 14 个 unittest 用例。

## 运行

```
python3.11 main.py
python3.11 -m unittest test_fairness -v
```

## 运行样例（`python3.11 main.py`）

```
=== Model A: worker (weak fairness violation) ===
[VIOLATED] weak fairness of 'reset'  (explored 2 states, exhausted=True)
           stem:  (empty)
           cycle: idle --work--> busy  busy --work--> idle
           enablement of 'reset': continuously enabled (2/2 cycle states), never taken in cycle
           replay: OK (loop re-verified, repeatable)

=== Model B: ping-pong (intermittent != continuous) ===
[HOLDS   ] weak fairness of 'recover'  (explored 2 states, exhausted=True)
[VIOLATED] strong fairness of 'recover'  (explored 2 states, exhausted=True)
           stem:  (empty)
           cycle: s0 --ping--> s1  s1 --pong--> s0
           enablement of 'recover': intermittently enabled (1/2 cycle states), never taken in cycle
           replay: OK (loop re-verified, repeatable)

=== Model C: toggle (fairness holds) ===
[HOLDS   ] weak fairness of 'reset'  (explored 2 states, exhausted=True)
[HOLDS   ] strong fairness of 'reset'  (explored 2 states, exhausted=True)

=== Model D: worker with max_states=1 (search cut short) ===
[PENDING ] weak fairness of 'reset'  (explored 1 states, exhausted=False)
           search incomplete -> verdict stays pending
```

模型 B 展示了核心区分：同一动作 `recover` 间歇可用 —— 弱公平 **HOLDS**，
强公平 **VIOLATED**，见证记录为 `intermittently enabled (1/2)`。

## 测试结果（`python3.11 -m unittest test_fairness -v`）

```
test_holds_when_every_cycle_takes_the_action ... ok
test_pending_when_search_is_incomplete ... ok
test_unreachable_cycle_is_ignored ... ok
test_replay_rejects_confused_fairness_kind ... ok
test_replay_rejects_cycle_taking_the_action ... ok
test_replay_rejects_tampered_transition ... ok
test_replay_verifies_strong_witness ... ok
test_replay_verifies_weak_witness ... ok
test_continuous_enablement_also_violates_strong_fairness ... ok
test_intermittent_enablement_violates_strong_fairness ... ok
test_continuous_enablement_violates_weak_fairness ... ok
test_intermittent_enablement_is_not_a_weak_violation ... ok
test_self_loop_witness ... ok
test_weak_witness_never_takes_action_in_cycle ... ok

----------------------------------------------------------------------
Ran 14 tests in 0.001s

OK
```

## 重放验证（replay）

`replay(ts, witness)` 从初始状态逐步重放 stem 与 cycle，校验：

1. 每一步都是模型中的合法转移，且链条连续；
2. 循环闭合（可无限重复）；
3. 被约束动作在循环中从未执行；
4. 弱公平要求循环上**持续**可用，强公平要求至少一处可用；
5. 见证记录的可用模式（持续/间歇）与模型重新计算结果一致 ——
   间歇可用的强公平见证无法冒充弱公平见证（有专门测试覆盖）。
