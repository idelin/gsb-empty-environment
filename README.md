# txguard：更新前前值校验参考模型

纯 Python 3.11 标准库实现，本地夹具驱动，不接消息平台。

## 语义约定

- **字段级部分前值**：`before` 只校验提供的字段；`after` 只覆盖提供的字段，未提供字段保留当前值。
- **缺失字段不等于 null**：行中不存在的字段用哨兵 `MISSING` 表示，`before` 声明 `null` 不匹配缺失字段。
- **声明顺序校验**：同事务多次改同键时，后续 op 校验的是前一 op 推演后的状态，而非全部对旧快照。
- **事务原子性**：任一 op 冲突 => 事务整体失败，推演结果全部丢弃，绝不半提交。
- **冲突隔离**：失败事务触及的键被隔离；同键后续事件挂起等待 `resolve()`；独立键事务照常提交。
- **不盲目后写覆盖**：前值不匹配即冲突，绝不 last-write-wins。
- **冲突分类**：`stale_before`（迟到旧版本，before 匹配历史快照）、`missing_event`（疑似缺事件，与任何已知版本不符）、`row_missing`（期望的行不存在）。
- **最小冲突证据**：只返回不匹配的字段（期望前值 vs 实际值）。
- **重复提交**：`tx_id` 幂等去重，重复投递返回 `duplicate` 且不重复应用。

## 夹具格式

`fixtures/scenario.json`：每个事务含 `tx_id`、`commit_seq`、`ops`（`table`、`pk`、`before`、`after`）。

## 运行

```bash
python3.11 run_demo.py        # 演示场景
python3.11 -m unittest discover -s tests -v   # 测试
```

## 运行样例输出

```
== 1. 按提交序号处理夹具事务 ==
  tx-001 seq=1 -> committed
  tx-002 seq=2 -> committed
  tx-003 seq=3 -> committed
  tx-004 seq=4 -> committed
  tx-002 seq=5 -> duplicate (tx_id 已处理，幂等跳过)
  tx-006 seq=6 -> failed (事务整体失败，键已隔离)
    op#0 kind=stale_before
      字段 accounts.A.balance: 期望前值=100 实际=55
  tx-007 seq=7 -> committed
  tx-008 seq=8 -> held (等待隔离键解决: [('accounts', 'A')])
  [冲突隔离后] 状态:
    accounts/A v5: {'balance': 55, 'owner': 'bob'}
    accounts/B v3: {'balance': 80, 'owner': 'alice'}
    accounts/C v1: {'balance': 20, 'owner': 'carol'}
    隔离键: [('accounts', 'A')]

== 2. 解决隔离键 accounts/A，重试被挂起的事务 ==
  tx-008 seq=8 -> committed
  [解决后] 状态:
    accounts/A v6: {'balance': 56, 'owner': 'bob'}
    accounts/B v3: {'balance': 80, 'owner': 'alice'}
    accounts/C v1: {'balance': 20, 'owner': 'carol'}
    隔离键: 无

演示完成。
```

## 测试结果

```
Ran 15 tests in 0.001s
OK
```

覆盖：部分前值只校验提供字段、未提供字段保留当前值、缺失字段不等于 null、
同事务同键按声明顺序校验、事务原子性整体回滚、冲突隔离与独立键继续、
挂起事件等待解决、交换键、重复提交幂等、迟到旧版本 + 最小字段证据、
疑似缺事件、行缺失、禁止盲目后写覆盖。

## 结构

- `txguard/models.py` — 事务/操作/冲突证据模型，`MISSING` 哨兵
- `txguard/store.py` — 版本化行存储（当前值 + 历史快照）
- `txguard/engine.py` — 校验引擎：推演校验、原子提交、隔离/挂起/解决
- `txguard/loader.py` — 本地 JSON 夹具加载
- `fixtures/scenario.json` — 演示场景（含交换键、重复提交、迟到旧版本）
- `tests/test_engine.py` — 15 个 unittest 用例
