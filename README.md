# 证据回收系统（evidence-gc）

按**有效引用**、**法定无关的自定义保留期**和**显式冻结标记**计算可回收证据集合；
输出引用阻塞链并支持试算。纯 Python 3.11 标准库实现，全部数据为合成数据，证据可重放。

## 运行

```bash
python3.11 -m evidence_gc.demo            # 运行样例（合成数据全流程）
python3.11 -m unittest discover -s tests -t . -v   # 测试
```

运行样例与测试结果见 `SAMPLE_RUN.md`。

## 模型

- **事实**（均带稳定 ID 与版本，见 `evidence_gc/catalog.py`）：
  - `evidence.instance` v1：证据实例，删除单元为 `(eid, version)`；
  - `evidence.reference` v1：引用，钉住目标版本，可失效；
  - `freeze.marker` v1：显式冻结标记，可继承、可撤回；
  - `retention.policy` v1：自定义保留期（法定保留不在本系统范围）；
  - `gc.plan` / `gc.certificate` / `liveness.proof` v1。
- **事件溯源**：`Store` 只追加事件（`seq` 单调、`tick` 单调不减），状态由事件折叠得到，
  任意状态可重放（`Store.from_events`）。
- **时间**：逻辑时钟整数 tick，保证确定性。

## 规则（稳定 ID / 版本）

| 规则 | 语义 |
| --- | --- |
| R-RET-1 v1 | 自定义保留期：`created_tick + retention_ticks > now` 时保留（法定无关） |
| R-REF-1 v1 | 有效引用：active 且 `target_version == 目标当前版本`；；沿边保留目标 |
| R-FRZ-1 v1 | 显式冻结保留目标，并沿有效引用**继承**到被引用实例 |
| R-FRZ-2 v1 | 冻结可撤回；撤回后**重新计算**闭包 |
| R-CLO-1 v1 | 保留策略与引用版本联合求**最小不可回收闭包**（最小不动点） |
| R-GC-1 v1 | 可回收 = 全部实例 − 闭包；**过期但仍被活结论引用不可回收** |
| R-NEW-1 v1 | 提交前到来的新引用/新冻结/保留延长**必须阻止已计划删除** |
| R-CMT-1 v1 | 提交验证：代际一致、候选存在且哈希一致，否则拒绝（应用前验证版本） |
| R-PRF-1 v1 | 存活证明：每个存活实例有保留依据，且**无悬空依赖** |
| R-DRY-1 v1 | **试算不实际删除**；任何应用前必须验证版本 |

## 步骤（稳定 ID / 版本）

S1 标记代际（计划固化版本快照与候选集）→ S2 提交前验证 → S3 重算闭包 →
S4 删除/阻止 → S5 签发证书与存活证明。

## 关键设计

- **最小不可回收闭包**（`closure.compute_closure`）：以保留期根与冻结根为种子，
  按根因类型（保留/冻结）分别沿有效引用做 BFS，取满足规则的最小不动点；
  每个被保留节点记录**引用阻塞链**（根因 + 逐跳规则/引用 ID）。
- **标记代际与提交验证**（`reaper.create_plan` / `reaper.commit`）：计划携带
  `generation`、事件前缀哈希与候选内容哈希快照；提交时重算闭包，
  新引用到来使候选重新被保留即从删除集中移除并记录阻塞链（R-NEW-1）；
  代际过期或重复提交直接拒绝（R-CMT-1）。
- **可重放删除证书**：证书记录完整事件时间线；`replay_certificate` 重建存储、
  重新执行提交并比对证书哈希，保证证据可重放。
- **存活证明**：提交后校验每个存活实例有保留依据、每条有效引用指向存活实例，
  无悬空依赖；指向已删除目标的新引用在写入时即被拒绝。
- **交错枚举**（`interleave.enumerate_interleavings`）：枚举
  `引用新增 × 解冻 × 回收提交` 的 3! = 6 种交错，展示顺序对结果的影响
  （提交先于引用新增 → 目标已删、引用被拒；反之 → 删除被阻止）。

## 目录

- `evidence_gc/catalog.py` — 事实/规则/步骤的稳定 ID 与版本目录
- `evidence_gc/model.py` — 规范化 JSON、哈希、实例键
- `evidence_gc/store.py` — 事件溯源存储与命令验证
- `evidence_gc/closure.py` — 最小不可回收闭包与阻塞链
- `evidence_gc/reaper.py` — 试算、计划、提交验证、证书、存活证明、重放
- `evidence_gc/interleave.py` — 交错枚举
- `evidence_gc/demo.py` — 运行样例（合成数据）
- `tests/test_evidence_gc.py` — unittest 测试
