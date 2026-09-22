# 可更正的固定网格重采样器

这个实现把不规则采样映射到无限固定网格：

- 网格位置严格为 `origin + index * period`，内部使用 `Fraction`，不使用二进制浮点。
- 支持 `linear`（线性）和 `step`（右连续阶梯）两种模式。
- 每个输出点都带有原始样本 ID、计算方式、有效性、无效原因和修订号。
- 迟到样本、同时间更正样本和撤回都会对已订阅网格点产生修订。
- 封闭前缀后会释放插值工作集，同时保留历史样本用于审计。

## 快速开始

```python
from correctable_resampler import CorrectableResampler, Mode

r = CorrectableResampler(origin=0, period=1, mode=Mode.LINEAR, max_missing_interval=10)
r.ingest("a", 0, 0)
r.ingest("b", 4, 8)

p = r.point(2)
print(p.valid, p.value, p.sample_ids, p.basis)
# True 4.000000 ('a', 'b') linear
```

如果在 `2` 处放显式断点，则不能跨越 `(0, 4)` 得到 4：

```python
r.add_breakpoint(2)
p = r.point(2)
print(p.valid, p.reason)
# False UnknownReason.BREAKPOINT
```

撤回右端样本后，受影响的线性输出变为未知：

```python
r.watch([1, 2, 3])
r.retract("b")
for revision in r.drain_revisions():
    print(revision.index, revision.after.valid, revision.after.reason)
```

## 有效性规则

### 线性模式

输出点 `t` 只有在以下条件全部满足时才有效：

1. 存在左右两个活跃样本 `l.time <= t <= r.time`。
2. `t == l.time` 或 `t == r.time` 时直接采用该样本；否则执行线性插值。
3. 开区间 `(l.time, r.time)` 内没有显式断点。
4. `r.time - l.time <= max_missing_interval`；等于上限允许，超过上限整段缺测未知。
5. 不做左端之前或右端之后的外推。

因此，长缺测段不会被一条直线填满；断点也不会被跨过去。

### 右连续阶梯模式

- 网格点命中样本时直接采用该样本，`basis="observed"`。
- 未命中样本时采用不晚于该点的最新活跃样本。
- 从最新样本到输出点的跨度超过 `max_missing_interval` 时未知；判断为严格大于。
- 若最新样本之后、输出点之前或输出点位置存在断点，则不能把旧值带过断点。
- 第一个样本之前未知。

### 同时间样本与撤回

- 同一时间可以摄入多个样本；最新摄入的活跃样本获胜。
- 撤回最新样本后，较早的同时间样本重新生效。
- 样本 ID 全局唯一，不能复用；撤回一个已经从工作集释放的样本会失败。
- 撤回不是删除：历史记录仍标记为已撤回，用于审计和重放。

## 修订接口

先订阅需要维护的网格点，再摄入迟到数据或撤回：

```python
snapshot = {p.index: p for p in r.watch(range(0, 5))}

r.ingest("late", 4, 8)
for revision in r.drain_revisions():
    snapshot[revision.index] = revision.after
```

- `watch(indices)` 返回当前快照并订阅后续变化。
- `ingest`、`retract`、`add_breakpoint` 会递增修订号。
- 只有语义真的变化才生成 `Revision`；修订包含变化前后的完整 `GridPoint`。
- `drain_revisions()` 取出并清除待处理修订。
- `point(index)` 和 `query_range(start, end)` 是一次性查询，不会自动订阅。
- 消费者按修订更新本地快照后，即可得到完整网格；未知也是一种必须保留的有效状态。

运行端到端例子：

```bash
python example_consumer.py
```

## 精度与舍入

- `origin`、`period`、样本时间和值接受 `int`、十进制字符串、`Decimal`、`Fraction`。
- 不接受 Python `float`，避免 `0.1` 这类二进制浮点误差混入时间网格。
- 输出时间保留为精确 `Fraction`，可通过 `str(point.time)` 序列化。
- 输出值固定保留 6 位小数，使用 `ROUND_HALF_EVEN`（银行家舍入）。
- 插值先在有理数域精确计算，最后只舍入一次。

## 历史样本与封闭区间

`release_through(index)` 用于确认并释放一个网格前缀：

```python
result = r.release_through(2)
```

规则如下：

1. 边界是精确网格点 `time_at(index)`，该点及之前的网格输出视为已封闭。
2. 封闭点上如果还有未消费修订，调用会失败；先 `drain_revisions()`。
3. 未来网格点的订阅可以保留，释放时只删除封闭点订阅。
4. 默认保留时间晚于边界的全部样本和断点。
5. 为了连接边界后的区间，只保留一个边界样本：不晚于边界的最新活跃样本。
6. 如果该样本位于某个已封闭断点之前，则不保留，防止旧样本被带过断点。
7. 被移出工作集的样本进入归档；`retained_records()` 查看当前计算工作集，`history_records()` 查看含归档的完整样本记录（时间、值、撤回标记），对应的 `*_sample_ids()` 是便捷视图。
8. 封闭后的点不能再查询，晚到样本、断点或撤回也不能修改封闭时间及之前的数据。

这套规则把“计算所需最小状态”和“审计历史”分开：封闭区间不再参与插值，但历史样本仍可追溯。

## 随机分块对照

参考实现 `correctable_resampler/reference.py` 不维护增量状态，而是每次独立重放截至当前的完整事件序列并整段计算网格。测试把随机样本、撤回和断点切成随机大小的块喂给增量重采样器，每块之后逐点对照整段参考结果。

覆盖内容包括线性和阶梯模式、非整数周期、负时间、同时间多样本、撤回、断点、缺测上限、舍入和网格端点。

```bash
python -m unittest discover -s tests -v
```
