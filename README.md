# aggrev：可更正的聚合输出（版本 DAG + 原子确认 + 独立核验）

纯 Python 3.11 标准库实现，无消息平台：变更来自本地 JSON 夹具，每条含
事务 ID、表、主键、前后值和提交序号（`commit_seq`）。

## 语义

- **更正 = 原结果版本 + 差量**：`Correction` 携带 `parents`（基于的版本）与
  每表 `deltas`，绝不携带绝对值，因此旧版本更正只能追加差量，不能覆盖新结果。
- **版本依赖是 DAG 而非单链**：多个上游可对同一聚合结果并行发布（分支），
  上游刷新后以多个父版本合并（汇聚）。
- **缺中间更正则暂停**：下游发现父版本缺失时把更正挂起（`GapTracker`），
  缺口补齐后自动恢复应用，绝不乱加；与冲突检测（`ConflictDetector`，
  负责环依赖识别）完全分离。
- **发布/确认/checkpoint 原子关联**：`AtomicStore.commit` 全有或全无；
  同 ID 重发是幂等空操作（指纹一致），同 ID 不同内容判冲突，不会双加。
- **独立核验**：`verifier.recompute` 从原始变更按 `commit_seq` 重算并校验
  前像完整性（抓乱序、丢事件、撤回/再新增错位）；`verify` 另检查祖先闭包、
  DAG 无环、确认覆盖、checkpoint 不回退、环依赖拒绝未应用。
- **垃圾回收以确认为准**：只有被*所有*下游 ack 覆盖的旧修订才折入 base
  快照并回收，保留墓碑使迟到重发仍幂等。

## 结构

```
aggrev/
  models.py             ChangeEvent / Correction（指纹、版本号派生）
  store.py              AtomicStore：全有或全无提交 + 原子落盘(os.replace)
  publisher.py          发布方：版本 DAG、幂等发布、冲突、ack 登记
  downstream.py         下游：按版本链应用，应用+确认+checkpoint 一次提交
  gap_tracker.py        缺口等待（只负责暂停/恢复）
  conflict_detector.py  冲突检测（Tarjan SCC 找环依赖）
  upstream.py           变更事件 -> 更正（按 commit_seq 排序折叠差量）
  verifier.py           独立重算与一致性核验
  gc.py                 基于下游确认的垃圾回收
fixtures/               本地变更夹具（两个上游 A/B）
tests/test_aggrev.py    25 个 unittest 用例
```

## 运行样例

```
$ python3 -m aggrev
=== 1. 加载本地夹具（事务ID/表/主键/前后值/提交序号） ===
  source A: 6 个变更事件, source B: 4 个变更事件

=== 2. 多上游发布更正（版本依赖形成 DAG） ===
  发布 A-r1: parents=['V0'] deltas={'accounts': 170}
  发布 B-r1: parents=['V0'] deltas={'accounts': 270}  (与 A-r1 并行的分支)
  发布 A-r2: parents=['V:A-r1', 'V:B-r1'] deltas={'accounts': 60}  (合并两个分支)
  发布 B-r2: parents=['V:A-r2'] deltas={'accounts': 60}
  发布方当前值: {'accounts': 560}, heads=['V:B-r2']

=== 3. 下游按版本链应用（乱序 + 重复投递） ===
  receive A-r2  -> waiting   value={}  (missing parents: ['V:A-r1', 'V:B-r1'])  # 乱序：父版本还没到
  receive B-r1  -> applied   value={'accounts': 270}
  receive A-r1  -> applied   value={'accounts': 500}  [缺口补齐，恢复应用: ['A-r2']]  # 补齐缺口
  receive A-r1  -> duplicate value={'accounts': 500}  (identical revision already applied)  # 重复投递同一 ID
  receive B-r2  -> applied   value={'accounts': 560}
  下游 checkpoint: {'B': 4, 'A': 6}

=== 4. 确认 + checkpoint 原子关联，旧修订垃圾回收 ===
  下游确认版本: ['V0', 'V:A-r1', 'V:A-r2', 'V:B-r1', 'V:B-r2']
  回收修订: ['A-r1', 'A-r2', 'B-r1']
  回收后: base={'accounts': 500}, 存活修订=['B-r2'], 值不变={'accounts': 560}

=== 5. 独立从原始贡献重算核验 ===
  重算结果: {'accounts': 560}
  发布方值: {'accounts': 560}, 下游值: {'accounts': 560}
  核验通过：乱序/撤回/环依赖处理、原子关联、幂等均一致 ✔
```

## 测试结果

```
$ python3 -m unittest discover
Ran 25 tests in 0.008s

OK
```

覆盖：端到端一致性、DAG 分支/合并、缺口暂停与恢复、永不满足的缺口、
重复更正不双加、同 ID 不同内容冲突（发布方/下游两侧）、环依赖拒绝
（含自环与级联）、撤回再新增顺序、乱序事件按 commit_seq 重排、前像
不匹配检测、旧版本更正不覆盖新结果且 checkpoint 不回退、提交失败
全有或全无、持久化往返、GC 需全体下游确认、回收后重发仍幂等、核验器
发现篡改与缺漏。
