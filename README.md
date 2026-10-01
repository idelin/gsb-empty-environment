# 可复用屏障的有限状态模型与模型检测

对多参与者可复用屏障（reusable barrier）建立有限状态模型，并用显式状态
模型检测搜索安全性质与公平进展反例。仅使用 Python 3.11 标准库与
`unittest`。验证对象仅限本仓库给定的有限状态模型。

## 文件

- `barrier_model.py` — 屏障协议模型：状态、动作、迁移、不变式与迁移断言；
  含故障注入开关（正确模型全部关闭）。
- `model_check.py` — 模型检测器：BFS 安全搜索（最短反例）、参与者对称归约、
  基于 SCC 的公平活性检查（输出可重放套索）、确定性重放。
- `verify_reduction.py` — 归约策略参考核验：小参与者全交错 vs 对称归约。
- `run_demo.py` — 演示脚本（下方为真实输出）。
- `test_barrier.py` — 18 个场景化单元测试。

## 模型要点

- 参与者状态：`OUT / ACTIVE / WAITING / RELEASED / DOWN`；可在轮间加入、
  退出、取消等待、掉线/重连；成员离开或掉线时屏障动态缩容。
- 到达与放行通知都经过显式单槽异步通道（`arr_msg` / `rel_msg`），消息携带
  轮次标签，因此旧轮迟到消息在状态空间中可见。
- 轮次变更有唯一显式提交点 `commit`：原子地放行全部等待者、推进轮次、
  清空到达集、合并待定加入。
- 安全性质：`I1–I5`（状态不变式）+ `T1–T3`（迁移断言），覆盖
  - 同轮未齐不得放行（`T2`、`I5`）；
  - 旧轮到达不能计入新轮、新成员提前到达不可凑齐（`I2`）；
  - 每轮只放行一次，最后一人到达与退出并发亦然（`T1`）；
  - 上一轮延迟通知不能唤醒下一轮等待者（`T3`）。
- 活性性质：在弱公平（持续使能的动作组终将执行）下，没有参与者永远滞留在
  `WAITING/RELEASED`。反例输出为可重放套索（前缀 + 循环），而非有限前缀。

## 判定纪律

- `PASS`：完整闭包证书（全部可达状态已枚举，报告状态/迁移数）。
- `FAIL`：给出反例；安全反例为 BFS 最短，活性反例为可重放套索。
- `UNKNOWN`：状态预算耗尽。预算不足绝不声称无错误。

## 运行

```bash
python3.11 run_demo.py          # 演示
python3.11 verify_reduction.py  # 归约参考核验
python3.11 -m unittest test_barrier -v
```

## 运行样例（`run_demo.py` 实际输出节选）

```
1. Correct model, safety, full interleaving (n=3)
   verdict=PASS  states=16605  transitions=154977
2. Correct model, safety, symmetry reduction (n=3)
   verdict=PASS  states=3225  transitions=30120
3. Round-confusion bugs: shortest counterexamples (n=3)
   bug[stale arrival counted in new round]: verdict=FAIL
    violation=['I2:stale-arrival-counted:p1:wait_round=0:round=1']
    shortest counterexample (7 steps):
       0. join 0
       1. send_arrive 0
       2. proc_arrive 0
       3. join 1
       4. send_arrive 1
       5. commit
       6. proc_arrive 1
    replay: ok=True violation=[...I2...]
   bug[stale notification wakes new-round waiter]: verdict=FAIL
    violation=['T3:stale-notification-released-waiter:p0']   (8 步, 可重放)
   bug[commit without reset -> double release]: verdict=FAIL
    violation=['I3:...', 'I5:released-in-current-round:p0']  (4 步, 可重放)
4. Fair progress (liveness) of the correct model (n=3)
   weak-fair:   verdict=PASS  states=16605
   no fairness: verdict=FAIL  victim=p0
     lasso: prefix=11 steps, cycle=9 steps
     replay: closed=True victim_stuck=True
5. Liveness bug: blocking wait + commit never fires (n=2)
   verdict=FAIL  victim=p0
   lasso prefix: [('join', 0), ('send_arrive', 0), ('proc_arrive', 0),
                  ('join', 1), ('disconnect', 1)]
   lasso cycle:  [('reconnect', 1), ('join', 1), ('disconnect', 1)]
   replay: prefix_ok=True cycle_ok=True closed=True victim_stuck=True
6. Budget discipline: insufficient budget must not claim PASS
   safety  max_states=100: verdict=UNKNOWN states=101
   liveness max_states=100: verdict=UNKNOWN states=100
```

## 归约参考核验（`verify_reduction.py` 实际输出）

小参与者（n=2, n=3）下，对称归约与全交错参考在所有配置（正确模型 +
4 种故障变体）上判定一致，反例最短长度一致：

```
n=2 correct        full=PASS(   885 st) reduced=PASS(   465 st) -          OK
n=2 stale_arrival  full=FAIL(    97 st) reduced=FAIL(    57 st) ce=7/7     OK
n=2 stale_release  full=FAIL(   151 st) reduced=FAIL(    89 st) ce=8/8     OK
n=2 commit_no_reset full=FAIL(   17 st) reduced=FAIL(    11 st) ce=4/4     OK
n=2 no_commit      full=PASS(    77 st) reduced=PASS(    42 st) -          OK
n=3 correct        full=PASS( 16605 st) reduced=PASS(  3225 st) -          OK
n=3 stale_arrival  full=FAIL(   346 st) reduced=FAIL(   100 st) ce=7/7     OK
n=3 stale_release  full=FAIL(   693 st) reduced=FAIL(   187 st) ce=8/8     OK
n=3 commit_no_reset full=FAIL(   33 st) reduced=FAIL(    12 st) ce=4/4     OK
n=3 no_commit      full=PASS(   721 st) reduced=PASS(   161 st) -          OK
reduction reference check: all verdicts match the full interleaving
```

## 测试结果

```
$ python3.11 -m unittest test_barrier -v
test_tiny_budget_is_unknown_not_pass ... ok
test_unknown_never_reported_as_safe ... ok
test_full_interleaving_passes ... ok
test_reference_check_small_configs ... ok
test_symmetry_reduction_same_verdict ... ok
test_blocking_wait_without_commit_is_unfair_lasso ... ok
test_correct_model_fair_liveness_passes ... ok
test_unfair_scheduler_starves_waiter_lasso_replays ... ok
test_commit_only_once_per_round_globally ... ok
test_last_arrival_then_leave_releases_once ... ok
test_leave_before_last_arrival_shrinks_barrier ... ok
test_replay_of_clean_trace_has_no_violation ... ok
test_replay_rejects_disabled_action ... ok
test_each_bug_found_with_replayable_shortest_ce ... ok
test_stale_arrival_ce_is_round_confusion ... ok
test_delayed_release_notification_cannot_wake_next_round ... ok
test_disconnect_shrinks_barrier_and_allows_commit ... ok
test_stale_arrival_rejected_in_new_round ... ok
----------------------------------------------------------------------
Ran 18 tests in 5.087s

OK
```
