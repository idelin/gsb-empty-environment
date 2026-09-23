# revisioned-aggregate

聚合输出版本化更正系统（Python 3.11，仅标准库，unittest 测试，本地夹具驱动，不接消息平台）。

## 设计

- **变更来源**：`fixtures/*.json`，每条事件含事务 ID、表、主键、前后值、提交序号（`ChangeEvent`）。
- **聚合器**（`aggregator.py`）：按 `commit_seq` 排序、按事务分组折叠事件，每个事务产生一个**修订**（`Revision`）：含每个聚合键的**基版本**与**差量**。一个修订涉及多个键时有多个父版本，版本依赖构成 **DAG** 而非单链。
- **原子存储**（`store.py`，SQLite）：修订发布与 checkpoint 推进在**同一事务**提交；同 ID 重发直接跳过（幂等，不双加）；下游确认（ack）也落库。
- **下游应用器**（`downstream.py`）：状态严格分离——
  - `waiting-gap`：基版本未到，**暂停**等待，缺口补齐后自动按序补放，绝不乱加；
  - `conflict`：基版本已知但已过期（旧版本更正），**拒绝**，不能覆盖新结果；
  - `duplicate`：同 ID 重投，不二次加。
- **独立核验器**（`verifier.py`）：只从原始事件重算真值，对修订日志做拓扑排序核验：拒绝重复 ID、未知基版本、提交序号乱序、过期基版本、**环依赖**及数值不符（含撤回处理错误）。
- **垃圾回收**（`store.gc()`）：仅回收有下游 ack 依据且已非任何键头版本的旧修订，回收记录连同 ack 证据写入 `gc_log`。

## 运行

```bash
python3.11 run_demo.py                      # 端到端样例
python3.11 -m unittest discover -s tests -v # 23 个测试
```

## 目录

- `revisioned_aggregate/` — `events` / `model` / `store` / `aggregator` / `downstream` / `verifier`
- `fixtures/` — `changes_basic.json`（更正）、`changes_retract.json`（撤回再新增）、`revision_log_cycle.json`（环依赖负例）
- `tests/` — 聚合器、下游、核验器、存储/GC 四组测试
