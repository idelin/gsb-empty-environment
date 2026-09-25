# 一维多通道分段线性插值及逆查询

纯 Python 3.11 标准库实现，不使用 NumPy/SciPy。

## 文件

- `piecewise1d.py`：核心模型、不可变快照、证据账本、区间插值、导数区间和解析逆查询。
- `sample_scenario.py`：固定步骤的可重放样例，演示插删、校准、快照隔离与局部证据重建。
- `test_piecewise1d.py`：`unittest` 测试和小规模稠密参考实现。
- `sample_output.txt`：最近一次样例输出。

## 核心规则

- `PiecewiseModel(channel_count=...)` 显式输入通道数；每个节点也显式给出 `channel`。
- `Measurement(lower, upper)` 表示测量误差区间；`None` 表示未知值，不会当作零。
- 同坐标多个节点按 `priority` 降序、`version` 降序、`node_id` 升序决定有效版本。
- `hard_break=True` 的节点仍可在该坐标精确取值，但任何跨向该点两侧的区间都不可插值。
- 端点精确返回测量区间；域外查询报错，不做外推。
- 区间索引在每次插入、删除、恢复、校准后只处理受影响通道及相邻区间；未触碰段复用证据。
- 旧 `Snapshot` 保存不可变条目、段和证据引用，后续校准不改变旧查询结果。
- 逆查支持非单调多解；确定平台返回 `plateau` 区间，测量误差使整段可能命中时返回 `uncertain_range`。

## 运行

```bash
python3 -m unittest -v
python3 sample_scenario.py
```
