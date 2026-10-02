# 可复用屏障的有限状态模型与模型检查

Python 3.11，仅标准库 + unittest。对多参与者可复用屏障建立有限状态模型，
显式搜索安全性质违例与公平进展反例，输出可重放反例/套索或完整闭包证书。

## 文件

- `barrier_model.py` — 有限状态模型（正确版 + 两个缺陷变体）
- `model_checker.py` — BFS 安全性、SCC 公平进展套索、对称归约、重放器
- `verify.py` — 验证场景入口
- `test_barrier.py` — unittest 测试

## 模型

状态：`(rnd, released_upto, status[], notif[], atag[])`，参与者宇宙有限（`n`），
轮次上限 `max_round` 保证模型有限。动作：

- `join/leave`：轮间加入/退出；`cancel`：取消等待；
- `disconnect`：掉线并触发动态缩容（需求数随成员数减少）；
- `arrive`：按当前轮次打标签，旧轮到达不得计入新轮；
- `release+commit`：轮次变更的明确提交点（原子迁移：放行、轮次 +1、
  清空本轮到达记录、生成放行通知）；
- `deliver`：送达上一轮遗留通知；正确模型中旧轮延迟通知仅被丢弃，
  不能唤醒下一轮等待者。

监控性质（迁移级）：P1 同轮未齐不得放行；P2 旧轮到达不计入新轮；
P3 每轮至多放行一次（最后一人到达与退出并发也只放行一次）；
P4 旧轮延迟通知不能唤醒新轮等待者。

## 检查语义

- 安全性：BFS 全交错，首个反例即最短；队列清空且无违例才判 SAFE 并给出
  闭包证书（状态集 sha256 + 计数）；预算（`max_states`）不足判 PENDING，
  绝不声称无错误。
- 公平进展：完整可达图上 Tarjan 分解，找无放行、有人持续等待且满足
  逐动作弱公平的坏循环，输出可重放套索（stem+loop），而非只查有限前缀；
  `verify_lasso` 独立重放校验（回到起点、无放行、有人等待、弱公平）。
- 对称归约：参与者不可区分，按规范化代表元去重；小规模下与全交错参考
  互相核验（结论一致 + 规范化状态集相等）。
- 缺陷变体 `round_confusion`（提交点不清到达记录）与 `stale_notif`
  （旧通知唤醒等待者）用于演示反例搜索：缺陷变体给出最短反例，
  正确模型给出完整闭包证书。

## 运行样例

```
$ python3.11 verify.py
== 场景1 正确模型安全性 (n=3, max_round=3, 全交错) ==
  结论: SAFE  已探索状态: 2457  迁移: 19928
  完整闭包证书: closure sha256:146ebc82...f264 states=2457 transitions=19928

== 场景2 正确模型公平进展 (n=2, max_round=2, 逐动作弱公平) ==
  结论: LASSO  可达状态: 117
  stem: disconnect(P0) -> arrive(P1) -> release+commit -> arrive(P1) -> deliver(P1)
  loop: disconnect(P1) -> join(P1) -> arrive(P1) -> cancel(P1) -> arrive(P1) -> join(P0) -> disconnect(P0)
  套索重放校验通过：loop 回到起点、无放行、有人等待、弱公平。

== 场景3 轮次混淆缺陷 (round_confusion, n=2) ==
  结论: VIOLATION  违反性质: P2_stale_arrival
  最短反例（4 步）: disconnect(P0) -> arrive(P1) -> release+commit -> release+commit
  反例重放确认违反: P2_stale_arrival

== 场景4 旧轮延迟通知缺陷 (stale_notif, n=2) ==
  结论: VIOLATION  违反性质: P4_stale_notify
  最短反例（5 步）: disconnect(P0) -> arrive(P1) -> release+commit -> arrive(P1) -> deliver(P1)
  反例重放确认违反: P4_stale_notify

== 场景5 预算不足 (max_states=10) ==
  结论: PENDING —— 预算不足，搜索未完成，不声称无错误。

== 场景6 对称归约核验 (n=3, max_round=2) ==
  全交错: SAFE, 状态 945   归约后: SAFE, 状态 221
  结论一致: True  规范化状态集一致: True
```

场景 2 的套索是真实发现：动态成员/取消语义下，屏障在逐动作弱公平调度中
仍可能永不放行（掉线-重入与到达-取消交替使放行条件反复被破坏），
因此“必然进展”在该模型下不成立，工具输出可重放套索而非有限前缀。

## 测试结果

```
$ python3.11 -m unittest test_barrier -v
Ran 14 tests in 0.144s
OK
```

覆盖：正确模型 SAFE + 闭包证书；轮次混淆最短反例（4 步）与重放；
旧通知反例（P4）与重放；每轮至多放行一次；同轮未齐不可放行；
预算不足保持 PENDING；套索可重放（n=2、n=3）；归约与全交错一致性；
篡改轨迹被重放器拒绝。
