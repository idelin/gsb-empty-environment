# 可修订覆盖率账本 (Correctable Coverage Ledger)

设备在线记录是**半开区间 `[start, end)`**；组成员资格也是带**生效时间与规则版本**的
半开区间（右端可为“至今”）。账本支持：

- 三种查询：**并集**（至少一台在线）、**交集**（全体成员在线）、**至少 k 台在线**；
- 重复上报不多计：同一设备的区间先求并集；相同记录 id 直接拒绝；
- 成员退出、区间撤回（retract）、撤回后补录新版（correct）；
- 结果**发布后可修订**：给出可逆差量（gained / lost，差量守恒，可整体取反）；
- **旧观察快照**继续按发布时的旧记录、旧成员规则计算；
- 端点索引：局部改动只更新对应设备的两个有序索引，不扫描全部设备历史；
- checkpoint：把追加式修订日志序列化，恢复时逐条回放；`Fraction` 精确率值无损存储。

仅使用 Python 标准库（`dataclasses`、`bisect`、`fractions`、`json`、`unittest`）。

## 快速开始

```bash
PYTHONPATH=. python3 demo.py
PYTHONPATH=. python3 -m unittest discover -s tests -v
```

```python
from coverage_ledger import Ledger, query

led = Ledger()
led.add_membership("A", 0, None, rule_id="mem-A")   # 0 起在组，至今
led.add_membership("B", 0, None, rule_id="mem-B")
led.add_observation("A", 0, 6, observation_id="a")
led.add_observation("B", 4, 8, observation_id="b")

query(led.snapshot(), (0, 10), "union").coverage_rate          # 4/5
query(led.snapshot(), (0, 10), "at_least_k", k=2).coverage_rate # 1/5
query(led.snapshot(), (0, 10), "intersection").coverage_rate    # 1/5
```

零长度或反向窗口抛出 `InvalidWindow`（参数错误）。

## 修订与差量

```python
led.publish_report("R", (0, 10), "union")          # 发布，钉住当前修订号
seq = led.head
led.end_membership("B", 5, rule_id="mem-B-2")      # B 在 5 退出（新规则版本）
led.retract_observation("b", "B")                  # 撤回旧区间
led.add_observation("B", 3, 8, observation_id="b2")# 补录新版

published, current, delta = reconcile(led, "R")
delta.is_conserving        # old + |gained| - |lost| == new
delta.inverse()            # 反向差量，可把当前答案还原回发布答案
query(led.snapshot(seq), (0, 10), "union")         # 旧快照：旧记录 + 旧成员规则
```

`revision_impact(led, seq, window)` 报告一条修订**实际**改动的时间范围：

- `raw_changed_ranges`：旧/新记录并集的对称差；
- `effective_changed_ranges`：再与“新规则下该设备在组时段”求交——真正可能改变答案的部分；
- 成员类修订报告 `removed_ranges` / `added_ranges`（旧规则版本与新规则版本）。

## 设计要点

- `coverage_ledger/store.py`：每设备维护按 start、按 end 的两套有序索引
  （`bisect`）。窗口查询用两个二分定位候选再求交集；增删只改单台设备。
- `coverage_ledger/query.py`：扫描点取“在线端点 ∪ 成员规则端点”。相邻断点之间
  成员集合与在线集合恒定，逐段分类。相接区间 `[0,4)`+`[4,8)` 在同一断点的结束与
  开始不会互相抵消，覆盖连续，端点不重复计长。
- `coverage_ledger/ledger.py`：所有变更都是追加式、自带逆操作的 `Revision`；
  `rollback_to` 与 checkpoint 回放共用同一套 apply/inverse 逻辑。
- 非成员设备即使有在线上报也不计入覆盖率，但会在分段证据里以 `online_non_members`
  标出。

## 测试

- `tests/test_reference.py`：40 组随机成员规则/上报（含重叠与重复上报），对三种查询
  与独立的精确参考实现逐一比对（全量参考）；
- `tests/test_checkpoint.py`：checkpoint 恢复、发布钉扎、rollback；
- `tests/test_endpoints_and_locality.py`：相接端点、重复上报、局部改动隔离；
- `tests/test_deltas.py`：差量守恒、逆差量、成员规则裁剪后的实际修订范围；
- `tests/test_scenario.py`：题目给定数值（4/5、1/5）、退出/撤回/补录后的正确切段、
  零长度窗口参数错误。
