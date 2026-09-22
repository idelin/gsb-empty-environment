# status-rle

带来源索引的运行长度编码（RLE）设备状态历史库，Python 3.11+，零依赖。

## 语义约定

- 事件时间为整数；段为半开区间 `[start, end)`，`end=None` 表示开放到 +∞。
- 每条事件有稳定身份 `(source, seq)`；`time`/`status` 是可更正的载荷。
- 规范排序为 `(time, source, seq)`；同一时间戳冲突时，排序靠后的记录生效，
  被覆盖的记录仍保留在段的 `provenance` 中作为证据。
- 连续相同状态合并为一个段，但合并不丢弃中间重复记录——全部留在来源索引里。

## 接口

```python
from status_rle import StatusHistory, apply_patch

h = StatusHistory()
patch = h.insert("sensor", 1, time=0, status="A")   # 每次编辑返回 Patch
patch = h.correct("sensor", 1, time=2)              # 更正：原子单个补丁
patch = h.retract("sensor", 1)                      # 撤回

h.segments()        # 当前段列表，每段带 provenance 来源索引
h.status_at(t)      # 某时刻状态
h.segment_at(t)     # 覆盖 t 的段（含全部原始事件引用）
h.explain(t)        # (段, 生效事件)，回答“为什么是这个状态”
```

## 补丁消费

`Patch(removed, added)` 是基于值的增量：下游用 `apply_patch(snapshot, patch)`
即可维护副本，无需重新查询；补丁失配（过期/乱序）会抛 `ValueError`。
完整示例见 `examples/patch_consumer.py`（A@0、A@2、B@5 → 补 B@1 拆段 → 撤回恢复）。

## 测试

```sh
python3.11 -m unittest discover -s tests -v
```

每个编辑序列都经三条路径交叉核对：直接查询当前段、从初始快照重放全部补丁、
从存活原始事件用 `build_segments` 重算。覆盖首末事件撤回、同时间冲突、
单条记录、空流等边界。
