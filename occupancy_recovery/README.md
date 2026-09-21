# 设备占用区间恢复与峰值并发查询

纯 Python 标准库实现，不接外部数据库。输入是可能乱序到达的 `start/end`
端点记录；系统负责把同一占用 ID 的端点配对成半开区间 `[start,end)`，维护
撤回 / 更正 / 重复上报状态，并查询峰值并发及所有达到峰值的时间段。

## 运行

```bash
cd occupancy_recovery
python3 demo.py
python3 -m pytest -q
```

## 模型与状态

每个占用 ID 始终处于五种状态之一：

- `EMPTY`：尚未见到端点。
- `START_ONLY`：只有 start（孤立 start）。
- `END_ONLY`：end 先到或尚无合法 start（孤立 end）。
- `PAIRED`：已形成有效半开区间 `start < end`。
- `WITHDRAWN`：已撤回，不再接受端点且不产生贡献。

每次上报返回明确结果：`RECORDED`、`DUPLICATE`、`CORRECTED`、
`REJECTED_INVALID`、`REJECTED_WITHDRAWN`、`WITHDRAWN`、`NOT_FOUND`。

关键规则：

- `end <= start` 一律拒绝，且拒绝发生在索引更新之前，已有区间和状态不变。
- 孤立端点不写入并发索引，所以先到的 end 不可能制造负占用。
- 重复同值端点不改变状态。
- 端点更正先从索引移除旧区间，再写入新区间；撤回同理，旧贡献全部撤销。
- 查询返回的每个峰值段都带 `evidence`，即该段真正活跃的占用 ID 集合。
- 相邻时段即使并发数相同，只要占用集合换人（如 `[0,5)` 的 A 与 `[5,9)`
  的 B），也不会被错误合并。

## 为什么不需要每次重扫全部区间

工程实现维护两棵增量 Treap：

1. 事件点树（`EventIndex`）：每个区间 `[s,e)` 写入 `s:+1`、`e:-1`。
   节点保存子树净增量 `total` 与事件之后的最大前缀增量
   `interior_pmax`。一次增删只改两个时间点，更新路径上每个
   节点做 O(1) 聚合重算，期望复杂度 O(log n)。
2. 活跃区间树（`IntervalIndex`）：按 `(start, occupancy_id)` 排序，子树保存
   `max_end`。查询峰值段证据时用 stabbing 搜索并按 `max_end` 剪枝，只访问
   O(log n + 命中数) 个节点。

范围查询先通过树切分得到：

- 进入 `[lo,hi)` 时的占用（严格 `t<lo` 的事件前缀）；
- `lo` 边界点先关后开后的占用；
- `(lo,hi)` 内部事件子树的最大前缀。

因此峰值为 `max(进入占用, 边界后占用, 边界后占用 + 内部最大前缀)`。
输出全部峰值时间段时，只按中序遍历查询范围内的变化点；范围外区间不参与
扫描。参考实现 `occupancy/brute_force.py` 则对小数据逐端点扫描，测试用它与
增量实现做差分，随机 3000 次操作、多种有界/无界查询窗口持续比对。

## 代码结构

- `occupancy/records.py`：状态、操作结果与记录数据类型。
- `occupancy/treap.py`：通用有序 Treap 与只读范围切分。
- `occupancy/indexes.py`：事件增量索引和活跃区间证据索引。
- `occupancy/tracker.py`：配对状态机、原子更新、峰值查询。
- `occupancy/brute_force.py`：逐端点扫描参考实现。
- `demo.py`：可直接运行的端到端连贯样例。
- `tests/test_tracker.py`：配对、更正、撤回、非法拒绝、证据、随机差分测试。
