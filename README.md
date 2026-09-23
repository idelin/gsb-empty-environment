# 一致性快照初始同步（快照水位 + 变更缓存 + 幂等回放 + 原子发布）

纯 Python 标准库实现（3.11+，sqlite3 / unittest），变更来自本地 JSON 夹具，
不接任何消息平台。

## 问题与解法

全量初始同步的经典竞态：扫描快照期间源库仍在变更，直接「扫完再补」容易
双计（快照已含的变更被重复应用）或漏变更（扫描期间的写入丢失）。本实现：

1. **一致性水位**：快照在独立只读事务中打开，水位 `watermark = max(commit_seq)`
   与全部数据行取自同一快照视图，二者天然一致（`cdc_sync/source.py` 的
   `SnapshotReader`）。
2. **扫描期间缓存变更**：同步器每扫描一行就从本地事务日志 `cdc_log` 增量拉取
   `commit_seq > watermark` 的变更进 `ChangeCache`，扫描结束再兜底拉一次
   （`cdc_sync/sync.py`、`cdc_sync/cache.py`）。
3. **只回放水位之后**：缓存层拒绝 `commit_seq <= watermark` 的事件；目标层每行
   携带最近应用的 `seq`，仅当事件 `commit_seq` 严格更大才应用——快照包含的
   更新不可能再应用（防双计），重复投递与乱序旧事件被幂等跳过。
4. **删除最终生效**：删除在目标侧留墓碑（tombstone），扫描期间删除的行发布后
   不存在，且乱序到达的旧更新无法将其复活。
5. **失败不发布混合状态**：全部写入先落 `stg__*` 暂存区，发布时在**单个事务**内
   把整代 `stg__*` 换名为 `live__*`；快照或回放任何一步失败即丢弃暂存区，
   已发布状态保持旧一代原样。

## 变更事件

每条事件含：事务 ID（`tx_id`）、表（`table`）、主键（`pk`）、操作
（`insert/update/delete`）、前值（`before`）、后值（`after`）、提交序号
（`commit_seq`，由源库提交时分配、全局单调递增）。见 `cdc_sync/models.py`
与 `fixtures/changes.json`。

约定：同一事务对同一主键只保留最终状态（夹具生成时归并）。

## 运行

```bash
python3 demo.py                              # 运行样例（端到端场景演示）
python3 -m unittest discover -s tests -v     # 运行测试
```

样例演示六个环节：快照前提交 5 个事务 → 扫描期间并发注入 5 个事务
（更新/删除/插入/插入后又删除）→ 发布后目标与源库逐表一致 → 快照中途失败
不发布混合状态 → 重跑同步结果幂等。

## 目录

- `cdc_sync/models.py`  — 变更事件模型
- `cdc_sync/source.py`  — 源库：提交分配 `commit_seq`、写 `cdc_log`、一致性快照读
- `cdc_sync/cache.py`   — 水位后变更缓存（拒旧、去重、排序）
- `cdc_sync/target.py`  — 目标库：暂存区、幂等回放（行/墓碑序号）、原子换名发布
- `cdc_sync/sync.py`    — 编排：快照 → 缓存 → 回放 → 发布
- `cdc_sync/fixture.py` — 本地夹具加载与事务归并
- `fixtures/changes.json` — 变更夹具（pre_snapshot / during_scan 两阶段）
- `demo.py`             — 运行样例
- `tests/test_snapshot_sync.py` — 14 个 unittest 用例

## 说明

本实现覆盖初始同步窗口；生产系统中发布后应继续尾随日志进入增量阶段
（同一套 `apply_event` 幂等规则可直接复用）。
