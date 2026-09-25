# 证据回收引擎（evidence-reclaim）

按**有效引用**、**法定无关的自定义保留期**与**显式冻结标记**计算可回收证据集合。
保留策略与证据引用版本联合求解**最小不可回收闭包**；冻结**可继承、可撤回**；
清理采用**标记代际**与**提交验证**；输出**可重放删除证书**与**引用阻塞链**。

纯 Python 3.11 标准库实现，测试使用 `unittest`。样例数据全部为合成数据。

## 核心语义

- **最小不可回收闭包**（`evidence_reclaim/protection.py`）：
  - 根：未过自定义保留期的节点（`R-RETENTION-ROOT`）、带活动冻结标记的节点（`R-FREEZE-ROOT`）；
  - 闭包：受保护节点沿**有效（未撤销）引用**边传播（`R-LIVE-REF-ROOT`），
    冻结谱系上的传播记为**冻结继承**（`R-FREEZE-INHERIT`）；
  - 因此**过期但仍被活结论引用的节点不可回收**（`R-EXPIRED-LIVE-REF`）。
- **试算（dry-run）**：`ReclaimEngine.plan(now)` 只产生计划，不修改任何状态；
  每次试算递增**标记代际**（`R-GENERATION`）。
- **提交验证**：`ReclaimEngine.commit(plan)` 应用前验证版本（`R-VERSION-CHECK`）——
  存储版本必须等于计划基线、候选版本精确匹配、计划摘要未被篡改；
  **计划后到来的新引用必须阻止已计划删除**（`R-CAS-NEW-REF`），
  拒绝单携带最新阻塞链与新一代际提示。
- **冻结可撤回**：解冻事件后重新计算闭包（`R-FREEZE-REVOKABLE`）。
- **删除证书**：记录候选集、代际、前后状态根、规则摘要与**存活证明**
  （无悬空依赖，`R-NO-DANGLING`），可独立重放验证（`R-CERT-REPLAY`）。
- **稳定 ID 与版本**：事实（`node.v1`/`ref.v1`/`freeze.v1`/`event.v1`）、
  规则（`rule.v1`）、阻塞链步骤（`chainstep.v1`）、计划（`reclaim-plan.v1`）、
  证书（`reclaim-cert.v1`）均带稳定 ID（内容寻址 sha256）与 schema 版本。
- **事件溯源**：全部状态由只追加事件日志折叠而来，`EventLog.from_records` 可完整重放。

## 目录结构

```
evidence_reclaim/
  ids.py         规范化序列化与稳定 ID
  facts.py       事实模型（节点版本/引用/冻结/事件/存储）
  rules.py       规则注册表（稳定 ID + 版本）
  events.py      事件日志：准入校验（含悬空检查）、折叠、重放
  protection.py  最小不可回收闭包 + 引用阻塞链
  engine.py      试算、提交验证、删除证书、存活证明、证书重放
  interleave.py  引用新增/解冻/回收提交的交错枚举
  scenario.py    合成样例数据
demo/run_sample.py   运行样例
tests/test_evidence_reclaim.py  16 个 unittest 用例
```

## 运行

```bash
python3.11 demo/run_sample.py          # 运行样例（合成数据）
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 运行样例（节选）

```
3. 试算（dry-run，now=500）：最小不可回收闭包 vs 可回收集合
  计划 plan:fbf47c21bf89c5e6  代际=1  基线版本=v12
  受保护: ['ev1#v1', 'ev2#v1', 'ev3#v1', 'ev4#v1', 'ev7#v1']
  可回收: ['ev5#v1', 'ev6#v1']

4. 引用阻塞链（过期但不可回收的节点）
  ev2#v1 (expires_at=100):
    [step:...] ev1#v1  root:unexpired  规则 R-RETENTION-ROOT v1
    [step:...] ev2#v1  edge:live-reference via r1  规则 R-LIVE-REF-ROOT v1
  ev4#v1 (expires_at=100):
    [step:...] ev3#v1  root:freeze  规则 R-FREEZE-ROOT v1
    [step:...] ev4#v1  edge:frozen-inheritance via r2  规则 R-FREEZE-INHERIT v1

5. 新引用到来 -> 已计划删除被阻止（提交验证）
  提交结果: 拒绝 (reject:fc7f88157ec14e33)
    - 存储版本已变化：计划基线 v12，当前 v13
    - 候选 ev5#v1 已被新引用/冻结重新保护；计划后新增引用: ['r-new']
    阻塞链 ev5#v1: ev1#v1 -> ev5#v1

6. 解冻后重新计算 + 重新试算 + 提交
  可回收: ['ev3#v1', 'ev4#v1', 'ev6#v1']
  提交成功，删除证书 cert:959616ece6e16763
  存活证明: ok=True 悬空引用=[]  post_version=v15

8. 交错枚举：新引用 / 解冻 / 提交 的全部 6 种顺序
  顺序 commit -> new-reference -> unfreeze
    [OK ] commit: 提交成功，证书 cert:af027df2a0961f9d
    [NO ] new-reference: 引用被拒绝: 引用目标不存在或已回收（悬空）: ('ev5', 1)
  顺序 new-reference -> commit -> unfreeze
    [OK ] new-reference: 引用已接纳
    [NO ] commit: 提交被拒绝: ... 候选 ev5#v1 已被新引用/冻结重新保护 ...
  （其余 4 种顺序见 demo/run_sample.py 完整输出）
```

完整输出见 `demo/run_sample.py` 的运行结果。

## 测试结果

```
$ python3.11 -m unittest discover -s tests -v
test_dry_run_does_not_mutate ... ok                # 试算不实际删除
test_expired_but_live_referenced_chain ... ok      # 过期但被活结论引用不可回收
test_freeze_inheritance_chain ... ok               # 冻结可继承
test_minimal_nonreclaimable_closure ... ok         # 最小不可回收闭包
test_permanent_retention_never_expires ... ok      # 永久保留（无保留期）
test_commit_reclaims_and_proves_liveness ... ok    # 提交 + 存活证明
test_dangling_reference_rejected ... ok            # 禁止悬空引用
test_double_commit_rejected ... ok                 # 重复提交拒绝
test_new_reference_blocks_planned_deletion ... ok  # 新引用阻止已计划删除
test_stale_plan_after_unrelated_event_requires_new_generation ... ok  # 版本校验
test_tampered_plan_rejected ... ok                 # 计划篡改拒绝
test_unfreeze_recomputes_closure ... ok            # 解冻后重新计算
test_certificate_replay_verification ... ok        # 删除证书可重放
test_interleaving_permutations ... ok              # 交错枚举 6 种顺序
test_log_replay_state_root ... ok                  # 日志重放状态根一致
test_replay_is_idempotent_and_stable ... ok        # 步骤 ID 稳定
----------------------------------------------------------------------
Ran 16 tests in 0.04s
OK
```
