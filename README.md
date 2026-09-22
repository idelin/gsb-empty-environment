# 设备占用区间重建与峰值查询

这是一个纯 Python、无外部数据库和第三方依赖的示例项目。它从可能乱序到达的 `start` / `end` 记录恢复半开占用区间 `[start, end)`，维护峰值并发，并在查询结果中返回贡献峰值的占用 ID。

## 快速运行

```bash
python demo.py
python -m unittest discover -v
```

要求 Python 3.9+，只使用标准库。

## 配对语义

每个占用 ID 独立维护状态：

- `end` 可以先于 `start` 到达；未配对记录进入 `unmatched_starts` 或 `unmatched_ends`。
- 同 ID 重复且端点相同的结束或开始返回 `duplicate`，不重复改状态。
- `submit_start()` / `submit_end()` 可做单端点更正；`replace_interval()` 原子更正两个端点。
- `withdraw()` 撤回记录；已撤回 ID 的迟到记录返回 `record_revoked`，不会恢复占用。
- `end <= start` 不产生区间，也不会生成负数占用：
  - 若该 ID 已有有效区间，无效更正返回 `invalid_rejected`，旧区间保留。
  - 若仍未配对成功，候选记录进入 `invalid_records`，之后可用有效端点恢复。
- 配对成功后才把 `[start, end)` 加入动态索引。

`snapshot()` 将有效区间、孤立 start、孤立 end、无效候选分开列出。

## 查询结果

```python
from occupancy_recovery import OccupancyTracker

tracker = OccupancyTracker()
tracker.submit_start("A", 0)
tracker.submit_end("A", 5)
tracker.submit_end("B", 9)
tracker.submit_start("B", 5)

result = tracker.peak(0, 10)
```

`PeakQueryResult` 包含：

- `peak`：查询范围内的峰值并发。
- `periods`：所有达到峰值的极大半开时间段；相邻且峰值相同会合并，即使贡献 ID 在交界处变化。
- `evidence_segments`：按端点切分的证据段；每段直接给出贡献该峰值的 `occupancy_ids`。

因此 `[0,5)` 和 `[5,9)` 的峰值都是 1，交界处不重叠；`periods` 可合并为 `[0,9)`，证据仍分别指向 `A` 和 `B`。

## 增量索引如何避免每次重扫

`DynamicIntervalIndex` 使用随机平衡 Treap 维护端点坐标：

1. 每个 Treap 节点对应一个坐标 `x`，其数据属于初等段 `[x, next_x)`。
2. 节点保存该初等段当前的占用 ID 集合。
3. 子树根保存子树内最大并发，新增或删除区间通过 Treap split 找到规范化范围。
4. 对整个规范子树的加一/减一使用惰性成员标记，只更新根节点和子树最大值，不遍历区间覆盖的所有初等段。
5. 查询任意 `[lo, hi)` 时临时插入边界坐标，split 后可直接读取范围子树峰值；遍历时按子树峰值剪枝，只展开达到峰值的证据段。
6. 查询结束后删除临时边界，不把查询边界永久写入索引。

新增端点落在已有初等段内时，新段需要继承该段当前的占用 ID 集合；这是对局部单段贡献者的复制，不扫描全部区间。已有坐标上的区间增删是期望 `O(log m)` 的 Treap 操作，更正则是一次旧区间删除加一次新区间添加。查询复杂度为期望 `O(log m + 证据输出量)`，其中 `m` 是已存储端点数。

## 测试

- 确定性测试覆盖乱序配对、重复结束、撤回、端点更正、无效记录、半开边界、范围外更正和证据 ID。
- `tests/test_tracker.py` 内置独立的逐端点扫描参考模型，执行 500 组随机序列，将 Treap 索引的峰值、峰值时段、证据段和树不变量与扫描结果逐次比对。
