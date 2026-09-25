# pwlin — 一维多通道分段线性插值与逆查询

纯标准库（Python 3.11）实现，不依赖 NumPy/SciPy。支持动态插删节点、
每通道断点与测量误差区间、值/导数区间/来源节点输出、显式版本优先规则、
非单调多解与平台区间逆查、快照隔离、区间索引局部重建。

## 语义约定

- **端点精确**：查询恰好落在断点上时返回该节点的测量区间；域外查询抛
  `OutOfDomainError`（不外推）。
- **断点不可跨越**：段内查询只使用本段两个端点；断点处导数报告左右段导数
  区间的包络（`left_derivative` / `right_derivative` 同时给出）。
- **误差区间**：节点值为 `[lo, hi]`；插值得到值区间，导数为上下包络线斜率
  构成的区间。逆查询返回 `{x: lo(x) <= y <= hi(x)}` 的连通分量，因此任何
  在误差区间内采样的真值都被包含（测试中有性质验证）。
- **显式优先规则**：同坐标多版本节点，先比 `priority`（高者优先），再比
  `version`（高者优先）；同 `(priority, version)` 重复插入视为替换。
- **未知不为 0**：节点缺少某通道时，该通道曲线不含此坐标；空通道查询抛
  `NoDataError`，绝不隐式补 0。
- **证据失效**：每次有效变更（insert/delete/calibrate）递增 epoch，旧证据
  `is_valid()` 立即失效，重新查询生成带新 epoch 与来源节点的证据。
- **快照隔离**：`snapshot()` 捕获不可变状态，之后的校准不影响旧快照。
- **索引局部重建**：每通道维护段索引（断点列表 + 段值域包围盒），插删只
  重建相邻段（ telemetry 计数 `index.rebuilt_segments`，测试验证 ≤3）。
- **尺寸与索引显式输入**：稠密参考实现 `pwlin/reference.py` 要求显式传入
  数组长度 `n` 与采样数，与索引实现互不共享代码，用于小规模对照核验。

## 布局

- `pwlin/interval.py` — 闭区间工具
- `pwlin/model.py` — `Measurement` / `Node` / 异常
- `pwlin/index.py` — 段索引（局部重建）
- `pwlin/table.py` — `CurveTable` / `Snapshot` / 证据类型 / 求值与逆查
- `pwlin/reference.py` — 自写稠密参考实现（显式尺寸）
- `samples/replay.py` — 可重放操作样例（`python3.11 -m samples.replay`）
- `tests/` — unittest 套件

## 运行

```sh
python3.11 -m unittest discover -s tests -t . -v
python3.11 -m samples.replay
```

## 测试结果

最近一次运行：`Ran 42 tests ... OK`，覆盖：

- 端点精确 / 断点不跨越 / 导数区间包络（test_table.py）
- 显式优先规则与同键替换（test_table.py）
- 非单调多解、整段平台区间、区间目标、误差带加宽（test_inverse.py）
- 快照不受校准影响、快照证据保持有效（test_snapshot.py）
- 索引局部重建计数与全量重建一致性（test_index.py）
- 插入撤销旧证据并生成新证据、删除再恢复往返一致（test_mutation.py）
- 枚举小节点集对照稠密参考、误差区间采样真值包含性（test_reference.py）
- 操作日志重放确定性（含 JSON 往返）（test_replay.py）
