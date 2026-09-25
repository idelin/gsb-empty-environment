# 运行样例与测试结果

## 运行样例：`python3.11 -m evidence_gc.demo`

```text
========================================================================
证据回收演示（合成数据 · Python 3.11 标准库 · 全部可重放）
范围说明：仅自定义保留期 + 显式冻结；法定保留不在本系统范围
========================================================================

[1] 事实登记（均带稳定 ID 与版本）
  E1 合同扫描件: 版本[1] 保留期30t
  E2 鉴定报告: 版本[1, 2] 保留期10t
  E3 转账凭证: 版本[1] 保留期10t
  E4 聊天截图: 版本[1] 保留期5t
  E5 邮件导出: 版本[1] 保留期10t
  E6 附件包: 版本[1] 保留期5t
  E7 审计日志: 版本[1] 保留期100t
  E8 临时缓存: 版本[1] 保留期5t
  引用: R1 E1@v1->E2@v2, R2 E2@v2->E3@v1, R3 E3@v1->E4@v1,
        R4 E7@v1->E2@v1(钉住旧版本→无效), R5 E5@v1->E6@v1
  冻结: F1 -> E5@v1（沿 R5 继承到 E6@v1）

[2] 试算 preview@25（R-DRY-1：不实际删除，事件数=23 不变）
  可回收: ['E2@v1', 'E8@v1']
  已过期: ['E2@v1', 'E2@v2', 'E3@v1', 'E4@v1', 'E5@v1', 'E6@v1', 'E8@v1']
  不可回收闭包: ['E1@v1', 'E2@v2', 'E3@v1', 'E4@v1', 'E5@v1', 'E6@v1', 'E7@v1']

[3] 引用阻塞链（最小不可回收闭包，R-CLO-1）
  E1@v1 [R-RET-1: 自定义保留期至 tick 30（now=25，法定保留不适用）]
  E1@v1 [R-RET-1: 自定义保留期至 tick 30（now=25，法定保留不适用）] -> E2@v2 [R-REF-1 引用钉住 via R1]
  E1@v1 [R-RET-1: 自定义保留期至 tick 30（now=25，法定保留不适用）] -> E2@v2 [R-REF-1 引用钉住 via R1] -> E3@v1 [R-REF-1 引用钉住 via R2]
  E1@v1 [R-RET-1: 自定义保留期至 tick 30（now=25，法定保留不适用）] -> E2@v2 [R-REF-1 引用钉住 via R1] -> E3@v1 [R-REF-1 引用钉住 via R2] -> E4@v1 [R-REF-1 引用钉住 via R3]
  E5@v1 [R-FRZ-1: 显式冻结标记: F1]
  E5@v1 [R-FRZ-1: 显式冻结标记: F1] -> E6@v1 [R-FRZ-1 冻结继承 via R5]
  E7@v1 [R-RET-1: 自定义保留期至 tick 100（now=25，法定保留不适用）]

[4] 标记代际 gen1 计划（S1，token=gen1-s24-111704a099）
  候选: ['E2@v1', 'E8@v1']
  版本快照: seq=23 hash=5f6c5287652a...

[5] 新引用到来 R6: E1@v1 -> E8@v1（t=26，必须阻止已计划删除，R-NEW-1）

[6] 提交 gen1（S2 版本验证 → S3 重算 → S4 删除）
  删除: ['E2@v1']
  阻止: E8@v1 类别=referenced
    阻塞链: E1@v1 [R-RET-1: 自定义保留期至 tick 30（now=26，法定保留不适用）] -> E8@v1 [R-REF-1 引用钉住 via R6]

[7] 存活证明（R-PRF-1）: ok=True 悬空依赖=[] 无根存活者=[]

[8] 删除证书 cert-dc56a37ed4148b47（gc.deletion_certificate v1）
  证书哈希: 4b7e5b00fb35dfb9...  重放验证: verified=True

[9] 解冻 F1（t=27，R-FRZ-2 重新计算）: 可回收变为 ['E5@v1', 'E6@v1']

[10] 提交 gen2: 删除 ['E5@v1', 'E6@v1'] 证书 cert-fddbeb5369dd45e9 重放 verified=True

[11] 应用前版本验证（R-CMT-1）
  重复提交 gen1 被拒绝: 计划已提交: gen1-s24-111704a099
  过期代际计划被拒绝: 代际过期（R-CMT-1）：计划 gen3，当前 gen4
  （gen4 为当前代际）

[12] 交错枚举：引用新增 × 解冻 × 回收提交（3! = 6 种）
  顺序 add_ref → unfreeze → commit
    - 引用新增 R-XZ: X@v1 -> Z@v1（已登记）
    - 解冻 F1（目标 Y@v1，已撤回）
    - 回收提交 gen1: 删除[无] 阻止[Z@v1]
    - 收尾 gen2: 删除['Y@v1']
    最终存活: ['X@v1', 'Z@v1']
  顺序 add_ref → commit → unfreeze
    - 引用新增 R-XZ: X@v1 -> Z@v1（已登记）
    - 回收提交 gen1: 删除[无] 阻止[Z@v1]
    - 解冻 F1（目标 Y@v1，已撤回）
    - 收尾 gen2: 删除['Y@v1']
    最终存活: ['X@v1', 'Z@v1']
  顺序 unfreeze → add_ref → commit
    - 解冻 F1（目标 Y@v1，已撤回）
    - 引用新增 R-XZ: X@v1 -> Z@v1（已登记）
    - 回收提交 gen1: 删除[无] 阻止[Z@v1]
    - 收尾 gen2: 删除['Y@v1']
    最终存活: ['X@v1', 'Z@v1']
  顺序 unfreeze → commit → add_ref
    - 解冻 F1（目标 Y@v1，已撤回）
    - 回收提交 gen1: 删除[Z@v1] 阻止[无]
    - add_ref 被拒绝: 引用目标不存在: Z@v1（拒绝悬空依赖）
    - 收尾 gen2: 删除['Y@v1']
    最终存活: ['X@v1']
  顺序 commit → add_ref → unfreeze
    - 回收提交 gen1: 删除[Z@v1] 阻止[无]
    - add_ref 被拒绝: 引用目标不存在: Z@v1（拒绝悬空依赖）
    - 解冻 F1（目标 Y@v1，已撤回）
    - 收尾 gen2: 删除['Y@v1']
    最终存活: ['X@v1']
  顺序 commit → unfreeze → add_ref
    - 回收提交 gen1: 删除[Z@v1] 阻止[无]
    - 解冻 F1（目标 Y@v1，已撤回）
    - add_ref 被拒绝: 引用目标不存在: Z@v1（拒绝悬空依赖）
    - 收尾 gen2: 删除['Y@v1']
    最终存活: ['X@v1']

[13] 规则与步骤目录（稳定 ID / 版本）
  R-RET-1 v1: 自定义保留期：created_tick + retention_ticks > now 时证据被保留（法定保留不适用）
  R-REF-1 v1: 有效引用：active 且 target_version 等于目标当前版本；有效引用沿边保留目标
  R-FRZ-1 v1: 显式冻结：active 冻结标记保留目标实例，并沿有效引用继承到被引用实例
  R-FRZ-2 v1: 冻结可撤回：撤回后重新计算闭包，仅由冻结保留的实例转为可回收
  R-CLO-1 v1: 最小不可回收闭包：保留策略与引用版本联合求不动点，取满足规则的最小集合
  R-GC-1 v1: 可回收集合 = 全部实例 - 最小不可回收闭包；过期但被活引用者不可回收
  R-NEW-1 v1: 新引用/新冻结/保留期延长在提交前到来时，必须阻止已计划删除
  R-CMT-1 v1: 提交验证：代际一致、候选仍存在且哈希一致、版本快照一致，否则拒绝
  R-PRF-1 v1: 存活证明：提交后每个存活实例有保留依据，且每条有效引用指向存活实例
  R-DRY-1 v1: 试算（dry-run）只读不删除；任何应用前必须验证版本
  S1 v1: 标记代际：生成 plan，固化版本快照与候选集
  S2 v1: 提交前验证：代际、存在性、内容哈希、版本快照
  S3 v1: 重算闭包：新引用/解冻等到来后重新计算
  S4 v1: 删除：仅删除重算后仍可回收的候选，其余阻止并记录
  S5 v1: 签发：输出删除证书、阻塞链与存活证明
```

## 测试结果：`python3.11 -m unittest discover -s tests -t . -v`

```text
test_certificate_carries_id_version_and_rule_meta (tests.test_evidence_gc.CatalogTests.test_certificate_carries_id_version_and_rule_meta) ... ok
test_stable_ids_and_versions (tests.test_evidence_gc.CatalogTests.test_stable_ids_and_versions) ... ok
test_closure_exact_sets (tests.test_evidence_gc.ClosureTests.test_closure_exact_sets) ... ok
test_expired_but_referenced_not_reclaimable (tests.test_evidence_gc.ClosureTests.test_expired_but_referenced_not_reclaimable) ... ok
test_freeze_inheritance_and_revoke (tests.test_evidence_gc.ClosureTests.test_freeze_inheritance_and_revoke) ... ok
test_stale_version_reference_is_invalid (tests.test_evidence_gc.ClosureTests.test_stale_version_reference_is_invalid) ... ok
test_six_permutations_and_ordering_effect (tests.test_evidence_gc.InterleavingTests.test_six_permutations_and_ordering_effect) ... ok
test_certificate_replays_and_is_deterministic (tests.test_evidence_gc.ReaperTests.test_certificate_replays_and_is_deterministic) ... ok
test_dangling_reference_rejected (tests.test_evidence_gc.ReaperTests.test_dangling_reference_rejected) ... ok
test_dry_run_does_not_mutate (tests.test_evidence_gc.ReaperTests.test_dry_run_does_not_mutate) ... ok
test_liveness_proof_has_no_dangling (tests.test_evidence_gc.ReaperTests.test_liveness_proof_has_no_dangling) ... ok
test_new_reference_blocks_planned_deletion (tests.test_evidence_gc.ReaperTests.test_new_reference_blocks_planned_deletion) ... ok
test_recompute_after_unfreeze (tests.test_evidence_gc.ReaperTests.test_recompute_after_unfreeze) ... ok
test_stale_and_double_commit_rejected (tests.test_evidence_gc.ReaperTests.test_stale_and_double_commit_rejected) ... ok
test_tick_must_be_monotonic (tests.test_evidence_gc.ReaperTests.test_tick_must_be_monotonic) ... ok

----------------------------------------------------------------------
Ran 15 tests in 0.211s

OK
```
