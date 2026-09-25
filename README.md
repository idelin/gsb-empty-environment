# noisefloor — 按频带与场景维护的滚动噪声底模型

纯标准库实现（Python 3.11），样本数组与采样率由本地夹具提供，不接音频设备。

## 结构

- `noisefloor/model.py` — 核心模型 `NoiseFloorModel`
- `noisefloor/fixtures.py` — 确定性样本流夹具（采样率 16000 Hz，hop 256，帧长 16 ms）
- `replay.py` — 可重放样例（`python3.11 replay.py`，输出确定）
- `tests/test_noisefloor.py` — unittest 套件（`python3.11 -m unittest discover -s tests -v`）

## 语义

- 每个 `(scene, band)` 独立维护最近 `window` 个**有效**样本的滚动窗口；
  场景切换各自保留基线。
- 样本状态：`valid` / `transient` / `missing`；迟到标记可撤回已纳入的贡献
  （基线严格等同从未纳入），重复标记幂等，无值缺测样本不能标回有效。
- 分位数为精确线性插值（type-7），支持重复值与大窗口；每帧只做
  O(log W) 次比较的增量插入/删除，**禁止每帧全排序**（全排序仅出现在
  快照恢复与测试 oracle 中）。
- 全部样本被剔除时返回 `Estimate(estimable=False, value=None)`，绝不返回 0。
- 阈值结果（`Estimate` / `Detection`）绑定模型版本；模型演进后可用
  `correct_detections()` 更正此前检测。
- 资源回收以活跃查询水位为界：`acquire_query(oldest_frame)` 声明仍需
  回溯的最老帧，`reclaim()` 只回收水位之前且在窗口之外的历史；
  无活跃查询时回收窗口外全部历史。已回收帧的迟到标记为安全 no-op。

## 声明

- **延迟**：ingest/mark 为 O(log W) 次比较 + O(W) 列表移动；estimate/detect
  为 O(1)；reclaim 为 O(R)（R 为水位内保留帧数）。
- **数值容差**：`TOL = 1e-12`；等价历史（分块、乱序、保存/恢复）结果位级一致。
