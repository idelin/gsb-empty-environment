# bitemporal — 双时间矩形索引存储

纯 Python 3.11 标准库实现的双时间（bitemporal）存储：每条记录同时具备
**业务有效区间** `[valid_from, valid_to)` 与**录入版本**（观察版本），支持按指定
观察版本查询业务时刻值、矩形范围查询与来源解释，撤销不覆盖历史。

## 运行

```bash
python3.11 demo.py                          # 运行样例
python3.11 -m unittest discover -s tests -t . -v   # 运行测试（25 个）
```

## 设计

| 模块 | 职责 |
| --- | --- |
| `bitemporal/timeline.py` | 精确时间模型：`WallClock`（墙钟）/ `Instant`（时间点）/ `Duration`（持续时长）三种类型互不混用；`Timeline` 持有**合成偏移转换表**（名字 → 精确 tick 偏移），全程整数运算，无浮点舍入 |
| `bitemporal/model.py` | 不可变历史日志 `Record`、活体状态 `LiveRecord`、规范可见矩形 `Fragment`（业务区间 × 版本区间，均半开） |
| `bitemporal/decompose.py` | 规范分解：任一时刻可见记录 = 优先级最高者；并列时录入版本早者胜（规则显式、全序） |
| `bitemporal/index.py` | 每键一棵以**版本区间**构建的中心区间树，节点内按业务起点排序可二分；矩形查询只访问可能相交的矩形，**不扫描历史日志** |
| `bitemporal/store.py` | 事务（更新/撤销/优先级变更原子提交）、点查询、矩形范围查询、`explain` 来源解释 |
| `bitemporal/checks.py` | 独立校验器：绕开索引逐版本重放日志，检查覆盖、边界与历史不变性 |

关键语义：

- **半开区间**：业务区间与版本区间均为 `[from, to)`，边界不双命中；`valid_to=None`
  表示开放结束，可被后续更正切分。
- **原子事务**：先校验全部操作并只改工作副本，再计算矩形差分，最后一次性应用；
  任一操作失败则整个事务不产生任何影响（版本号也不前进）。
- **一次更正切分多个矩形**：新 upsert 与所有相交的前沿矩形做差分，被切分的旧矩形
  在当前版本关闭（`ver_to` 只写一次），新矩形自当前版本生效。
- **历史不变性**：后录入的追溯更正只追加新矩形、关闭旧前沿，已关闭矩形永不被修改，
  因此较早观察版本的查询结果永远不变；撤销（retract）同样只是新日志条目。
- **查询不扫全史**：点查询/范围查询只走该键的矩形索引；`QueryStats` 记录实际考察的
  矩形数，测试断言其远小于历史记录总数。

## 样例输出（`python3.11 demo.py` 节选）

```
== 2. Retroactive correction splits an open-ended rectangle ==
  alice @2024-06 as observed at version 1                    -> 100
  alice @2024-06 as observed at version 2 (correction)       -> 120

== 3. Retraction does not overwrite history ==
  alice @2024-06 at latest (correction retracted)            -> 100
  alice @2024-06 as observed at version 2 (unchanged)        -> 120

== 5. Rectangle range query with provenance ==
  valid [2024-01-01T00:00:00[CST], open) x versions [v1, v2)  value=100  priority=0  source=record #1
  valid [2024-03-01T00:00:00[CST], 2024-09-01T00:00:00[CST]) x versions [v2, v3)  value=120  priority=10  source=record #2
  ...
  QueryStats(examined=6, matched=6); history log holds 5 records (query never scans the log)

== 7. Independent per-version invariant check ==
  OK: 1 keys, 4 version slices, 6 rectangles verified
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -t .
.........................
----------------------------------------------------------------------
Ran 25 tests in 1.248s

OK
```

覆盖：时间三类型区分与精确换算、开放区间切分、边界不双命中、显式优先级与并列裁决、
追溯更正不改变较早观察版本、撤销保留历史并使被压制记录复现、单事务多矩形切分、
事务原子回滚、矩形范围查询与来源解释、点查询考察量有界（不扫全史）、脚本化场景与
80 事务随机模糊测试的逐版本独立重放校验。
