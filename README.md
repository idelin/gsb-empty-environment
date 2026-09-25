# timeline: 变长块的样本↔时间映射

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确算术），不接任何音频设备；
样本数组与采样率全部由本地夹具 `timeline/fixtures.py` 提供。

## 语义约定

- 所有区间均为半开 `[t_start, t_end)`：查询恰好落在块边界上不会重复样本。
- 样本是位于 `t_i = base_time + 校准偏移 + i / sample_rate` 的**点**；
  每个块自带采样率，采样率切换即新块，绝不沿用旧换算率。
- 块覆盖 `[t_0, t_0 + n/rate)`；重叠时 **version 高者优先**，同 version 按
  `block_id` 字典序大者优先（显式、确定性的选择），生成互不相交的切片。
- 空洞保留证据：`reason="gap"`（无任何块覆盖）与 `reason="rounding"`
  （胜出的块在该区间没有样本点）。切片的 `lead`/`overshoot` 字段记录边界与
  最近样本点的偏差（均 ≥ 0，对齐时恰为 0），即舍入证据。
- 新增校准（`Timeline.apply_calibration`）可改变历史块映射：整体重建索引并
  **原子交换**（单次引用赋值 + 锁），旧游标/旧快照继续读到旧映射，保持稳定。
- 多级索引（粗粒度桶 + 细粒度切片）不假设输入时间单调：块可任意顺序插入，
  索引在构建时显式按时间排序；正确性由独立的逐样本朴素对照验证（含撤回）。

## 延迟与容差声明

- 构建：`O(B log B + E)`，B 为块数，E 为事件点数（≤ 2B）；内存 `O(S)`，S 为切片数。
- 查询 `slice_at` / `slices_in` / `read`：`O(log S + k)`，k 为命中切片数；
  `read` 另需 `O(m)` 复制返回的 m 个样本。`time_range_of` / `sample_range_of` 为 `O(1)`。
- 校准更新 / 增删块：全量重建 `O(B log B)`，交换本身 `O(1)` 且原子。
- 数值容差：内部全部使用 `Fraction` 精确计算；仅在展示层转 float，
  float 时间比较的统一容差为 `TOLERANCE_SECONDS = 1e-9` 秒。

## 运行

```sh
python3.11 -m timeline.demo      # 运行样例
python3.11 -m unittest discover -s tests -v   # 测试（16 个用例）
```

## 结构

- `timeline/core.py` — 块、校准、切片/空洞、两级索引、游标、Timeline 门面。
- `timeline/fixtures.py` — 本地样本数组与采样率夹具（4/2/8 Hz 小速率便于核对）。
- `timeline/demo.py` — 运行样例：切片解析、空洞、双向查询、校准更新、撤回。
- `tests/test_timeline.py` — 边界、版本优先、空洞、校准/游标、撤回，
  以及随机小数据对逐样本朴素参照的全量对照。
