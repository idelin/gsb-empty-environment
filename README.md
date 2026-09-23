# cdc-verify：先验证前值再应用的变更处理参考模型

纯 Python 3.11 标准库实现（无第三方依赖、不接消息平台）。变更来自本地
JSON 夹具，每条事件携带：事务 ID（`txn_id`）、表（`table`）、主键（`pk`）、
部分前值（`before`）、部分后值（`after`）和提交序号（`commit_seq`）。

## 核心语义

- **不盲目后写覆盖**：每个事件的 `before` 先与当前状态比对，通过后才应用
  `after`；`commit_seq` 倒退（迟到旧版本）或复用也会拒绝。
- **字段级部分前值**：`before` 只验证其列出的字段；未列出的字段既不校验，
  也不在合并 `after` 时被改动（保留当前值）。
- **同事务同键按声明顺序验证**：同一事务内对同键的多个事件，逐条在影子
  副本上验证并应用，后一条看到的是前一条的结果，而非事务前的旧快照。
- **缺失字段 ≠ null**：当前行没有某字段时，`before` 里写 `null` 也不匹配，
  证据中记为 `<missing>`；行中字段为 `null` 且 `before` 为 `null` 才算匹配。
- **缺事件/冲突**：对不存在的行给出 `before` 报 `missing_row`；对存在的行
  执行插入报 `insert_exists`；删除不存在的行报 `delete_missing`。

## 原子性与冲突隔离边界

- **事务是原子单位**：事务内任一事件冲突，整个事务拒绝，存储保持原样
  （影子副本丢弃），同事务其他键不会部分落库。
- **冲突隔离不阻断流**：冲突键进入隔离（quarantine）并附最小字段证据；
  键不相交的独立事务继续正常应用。
- **同键后续事件等待解决**：触及隔离键的事务整体入队等待（即使其中的
  其他键是干净的，也保持原子性不提前应用）。
- **解决策略**：`accept_current` 以当前状态为基线重写等待事件的 `before`
  后按序重放（重放中再冲突则重新隔离、停止排空）；`discard` 丢弃该键的
  等待事务并解除隔离。

## 参考模型枚举的边界情形

| 情形 | 行为 | 夹具/测试 |
| --- | --- | --- |
| 交换键（同事务两键互换值） | 各自按键验证，正常应用 | `fixtures/04_swap_keys.json` |
| 重复提交（同指纹事件） | 幂等跳过，记 `duplicate` | `fixtures/05_duplicate_commit.json` |
| 迟到旧版本（`commit_seq` 倒退） | 拒绝为 `late_version`，不覆盖新值 | `fixtures/06_late_old_version.json` |
| `commit_seq` 复用（不同事件同序号） | 拒绝为 `seq_reuse` | `LateVersionTest` |
| 最小冲突证据 | 只报不匹配的字段（字段、期望值、实际值） | `MinimalEvidenceTest` |

## 目录结构

- `cdc_verify/model.py` — 事件、冲突证据、报告等数据模型（含 `MISSING` 哨兵）
- `cdc_verify/processor.py` — 验证-应用处理器、隔离与解决逻辑
- `fixtures/*.json` — 8 个本地夹具（种子行 + 事务/解决步骤）
- `demo.py` — 顺序执行全部夹具并打印判定与证据
- `tests/test_processor.py` — 26 个 unittest 用例

## 运行

```sh
python3.11 demo.py                 # 运行全部夹具
python3.11 -m unittest discover -v # 运行测试
```

## 运行样例（`python3.11 demo.py`，节选）

```text
== 03_conflict_isolation_atomicity.json: conflict_isolation_and_atomicity_boundary
  txn t1   -> rejected
  txn t2   -> applied
  txn t3   -> blocked
  resolve accounts/A [discard] -> discarded
  txn t4   -> applied
  conflict: {"txn_id": "t1", "table": "accounts", "pk": "A", "commit_seq": 1, "kind": "before_mismatch", "fields": [{"field": "v", "expected": 9, "actual": 1}], "detail": ""}
  blocked:  {"txn_id": "t3", "waiting_on": ["accounts", "A"], "reason": "before_mismatch"}
  resolved: {"key": ["accounts", "A"], "strategy": "discard", "conflict": "before_mismatch", "dropped_txns": ["t3"]}
  final store: {"accounts/A": {"v": 11}, "accounts/B": {"v": 2}, "accounts/C": {"v": 30}}

== 07_missing_field_vs_null.json: missing_field_is_not_equal_to_null
  txn t1   -> rejected
  txn t2   -> applied
  txn t3   -> applied
  conflict: {"txn_id": "t1", "table": "users", "pk": "A", "commit_seq": 1, "kind": "before_mismatch", "fields": [{"field": "email", "expected": null, "actual": "<missing>"}], "detail": ""}
  final store: {"users/A": {"name": "n1"}, "users/B": {"name": "n2", "email": "b2@x"}}

== 08_resolution_accept_current.json: same_key_events_wait_then_rebase_on_resolution
  txn t1   -> applied
  txn t2   -> rejected
  txn t3   -> blocked
  resolve docs/A [accept_current] -> replayed
  resolved: {"key": ["docs", "A"], "strategy": "accept_current", "conflict": "before_mismatch", "replayed_txns": ["t3"], "failed_txn": null}
  final store: {"docs/A": {"v": 6}}
```

要点解读：`03` 中 t1 因键 A 前值不匹配被整体拒绝（同事务的键 B 未落库），
独立事务 t2 照常应用，触及隔离键的 t3 入队等待，discard 后 t4 才继续；
`07` 中 `email` 缺失与 `null` 被区分（`<missing>` 证据）；`08` 中 t3 等
待冲突解决后被重定基到当前值并按序重放。

## 测试结果

`python3.11 -m unittest discover -v`（Python 3.11.15）：

```text
Ran 26 tests in 0.001s

OK
```

覆盖：部分前值验证与保留、同事务同键顺序验证、事务整体失败与独立事务
继续的边界、同键等待与两种解决策略、重放再冲突重新隔离、交换键、重复
提交幂等、迟到旧版本与序号复用、最小冲突证据、缺失字段与 null 的区分、
缺行/重复插入/删除缺失等。
