# polytopo — 多边形共享边邻接分析

纯 Python 3.11 标准库实现。所有坐标与拓扑判断均使用 `fractions.Fraction`
精确运算，**不使用任何 epsilon 近似**；容差仅出现在显式的吸附（snapping）
API 中，且比较是精确的有理数比较。

## 功能

- `analyze_adjacency(poly_a, poly_b)`：判定两个多边形边界的共享边邻接，
  返回 `Adjacency`：
  - `shared_segments`：极大共享线段（精确有理数端点，含 `length` /
    `length_squared`）。
  - `shared_length`：共享边界总长度（有理数时为 `Fraction`，无理数长度
    退化为 float，但 `length_squared` 始终精确）。
  - `point_contacts`：孤立的点接触，单独分类。
  - `is_edge_adjacent`：是否存在正长度共享边。**只共一个点不算边邻接。**
- `snap_vertices(vertices, targets, tolerance)` /
  `snap_polygon_to_polygon(moving, anchor, tolerance)`：容差吸附。

## 规则与退化情形定义

- 坐标为整数或有理数（`int` / `Fraction` / 可解析字符串）。
- 零长度边（连续重复顶点）：丢弃，不影响结果。
- 共线连续顶点（180° 顶点）：允许；插入或删除此类顶点结果不变。
- **重复边 / 回描边不双算**：共享长度是边界交集的测度——重叠区间先在
  支撑线上取并集再求和，同一条边出现多次只计一次。
- **长边对多短边**：对方一条边被共线顶点拆成多段时，相邻区间合并为
  一条极大共享线段，总长度正确汇总。
- 点接触（角对角、T 型顶点落在对方边上、边界穿越交点、共线边仅端点
  相接）单独列入 `point_contacts`，不构成边邻接；位于共享线段上（含
  端点）的点不重复列为点接触。
- 少于 3 个顶点的输入抛出 `ValueError`；自交多边形不额外校验，按其
  边环处理。

## 容差吸附契约

- 判定为 `distance² <= tolerance²` 的精确有理数比较，无 epsilon。
- 每个被移动的顶点产生一条 `SnapEvent`，精确报告 `displacement`（位移
  向量）与 `distance_squared`。
- 不在容差内的顶点**逐位不变**——吸附不会改变无关拓扑。
- 等距并列时取目标索引最小者，结果确定。

## 运行

```sh
python3.11 demo.py            # 运行样例
python3.11 -m unittest -v     # 测试
```
