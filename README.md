# 双时间轴版本索引（Bitemporal Version Index）

“**10 时发生了什么**”和“**当时我们知道什么**”是两类不同的查询。本库用一个
只追加账本同时回答它们：

| 查询 | 固定 | 变化 | API |
|---|---|---|---|
| 轴一：发生了什么 | 业务时间 `t` | 观察时刻 `as_of` 推进，回放认知演化 | `evolutions(obj, t)` / `get(obj, t, as_of)` |
| 轴二：当时知道什么 | 观察时刻 `as_of` | 沿业务时间展开当时已知的整条时间轴 | `timeline_as_of(obj, as_of)` |

需要 Python 3.11+，仅用标准库。

## 数据模型

每条记录（`bitemporal.Record`）同时带两套时间：

- `vstart, vend`：**业务有效区间**，统一左闭右开 `[vstart, vend)`，
  `vend = bitemporal.INF` 表示开放终点；
- `recorded_at`：**录入（生效）时间**。查询携带观察时刻 `as_of`，
  只能看到 `recorded_at <= as_of` 的证据，未来版本对旧查询不可见；
- `op`：`ASSERT`（录入/更正/补录一个值）或 `RETRACT`（撤销，值为空）；
- `replaces_id`：直接前驱。更正/撤销必须同对象、同业务区间，且
  `recorded_at` 不早于前驱。

更正、撤销、补录都是**追加新记录**，历史记录永不就地改写，因此
`as_of=25` 的旧查询永远不会被 30 时刻才录入的新值污染。

## 同对象重叠记录的优先级（不依赖到达/遍历顺序）

1. **链式失效**：候选中若有记录的 `replaces_id` 指向另一条候选，被指向者
   先剔除（它已被可见后继取代）；
2. 链头之间按 `(recorded_at, rid)` 字典序取最大——录入更晚的证据胜出，
   同一时刻录入用 `rid` 确定性破并列；
3. 胜出记录是 `RETRACT` 则结果为“已撤销”（无值）；没有候选则为“未知”。

独立重叠记录（无取代关系）也由此规则得到唯一答案，和谁先插入无关；
测试 `test_insert_order_independence` 用打乱的追加顺序验证了这一点。

## 查询示例

```python
from bitemporal import VersionIndex, INF, Op, Record

idx = VersionIndex()
idx.append_many([
    Record("s1", "sensor", 10, 20, 20, Op.ASSERT, "A"),       # 10时发生，20录入 A
    Record("s2", "sensor", 10, 20, 30, Op.ASSERT, "B", "s1"), # 30更正为 B
])

idx.get("sensor", 10, 25).value  # 'A'  —— 30 的更正在 25 时尚未录入
idx.get("sensor", 10, 35).value  # 'B'
idx.evolutions("sensor", 10)     # [(20, A), (30, B)]：认知如何变化
idx.timeline_as_of("sensor", 25) # [Segment(10, 20, present, 'A')]
idx.evidence("sensor", 10, 35)   # 全部证据（含被取代的 s1）
```

## 索引结构

每个对象维护三列按 `(时间, rid)` 有序的数组（`vstart`、有限 `vend`、
`recorded_at`），点查用二分取集合差：

```
覆盖(t)     = {vstart <= t} - {vend <= t}     # 左闭右开
可见(as_of)  = {recorded_at <= as_of}
候选        = 覆盖(t) 且 可见(as_of)
```

索引支持按对象 + 业务时间 + 观察时刻定位（`get` / `evidence`），
以及按对象整轴扫描（`timeline_as_of` / `evolutions`）。

## 参考查询与随机版本序列测试

`bitemporal.reference` 提供完全不读索引结构的朴素线性扫描
（`ref_get` / `ref_timeline_as_of` / `ref_evolutions`），作为标准答案。
`tests/test_bitemporal.py` 生成 300 条随机版本序列（链式更正/撤销、
独立重叠、开放区间、未来版本），在 `(t, as_of)` 查询网格上逐点对比两套
实现，并验证：

- 索引结果与插入顺序无关；
- 回收后所有 `as_of >= horizon` 查询与时间轴结果不变。

```bash
python3.11 -m pytest tests/ -q
python3.11 demo.py
```

## 旧版本保留与回收条件

**默认永久保留**：旧观察时刻会重放“当时知道什么”，被覆盖的记录仍是旧
查询的答案来源，不能就地删除。

设归档后只需服务 `as_of >= horizon` 的查询。一条记录可**物理回收**，
当且仅当它存在一条直接后继，且后继的 `recorded_at <= horizon`。此时对所有
仍支持的观察时刻，后继恒可见且同区间，旧记录永远是链中节点、不可能成为
链头，故不影响任何查询。独立重叠记录没有后继，永不可据此回收；未来版本
（`recorded_at > horizon`）的前驱暂不可回收。回收建议先导出冷存储
（保留墓碑元数据以维持替换链可校验），再调用 `purge(idx, horizon)`。

## 目录

```
bitemporal/model.py      # Record/Segment/状态枚举/确定性胜出规则
bitemporal/index.py      # VersionIndex：三列有序数组 + 二分点查
bitemporal/reference.py  # 不依赖索引的线性扫描参考实现
bitemporal/retention.py  # 可回收判定与 purge
bitemporal/fixtures.py   # 合成夹具（更正/追溯撤销/补录/相接/未来版本…）
demo.py                  # 两条时间轴的查询演示
tests/test_bitemporal.py # 随机版本序列差异测试
```
