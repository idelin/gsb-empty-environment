# 可分解聚合：本地 / 全局两阶段

纯 Python 3.11 标准库实现，不调用任何 SQL 引擎。

## 语义约定

- 表是行的**多重集合**：重复行重复计数，不去重。
- NULL 用 `None` 表示；除 `COUNT(*)` 外所有聚合忽略 NULL。
- NULL 语义与空表一致：空集上 `SUM/AVG/MIN/MAX -> None`，`COUNT -> 0`。
- `AVG` 的中间状态是 `(sum, count)`：不能平均分片均值；空分片贡献
  `(0, 0)`，不进入分母。
- 全 NULL 列的 `SUM` 返回 `None`（SQL 的 NULL），不是 0。
- `COUNT DISTINCT` 需要额外状态（值集合），全局阶段取并集。

## 文件

- `aggregation.py` — 两阶段执行引擎（`local_phase` / `merge` / `global_phase`）
- `example.py` — 合成关系表上的运行样例
- `test_aggregation.py` — unittest 测试（18 例）

## 运行

```sh
python3.11 example.py
python3.11 -m unittest -v test_aggregation.py
```
