# 动态点集凸包（精确算术）

Python 3.11，仅标准库（`fractions` / `bisect`），测试用 `unittest`。

## 文件

- `convex_hull.py` — `DynamicConvexHull` 实现，直接运行 `python3.11 convex_hull.py` 可看演示
- `test_convex_hull.py` — 25 个单元测试，`python3.11 -m unittest -v`

## 核心约定

- **精确性**：坐标只接受 `int` / `Fraction`，内部统一为 `Fraction`；
  转向、共线、极值比较全为精确有理数运算，无 epsilon。`float`/`bool` 直接 `TypeError`。
- **重复坐标多 ID**：`coord -> [ids]`；删除一个 ID 只删该 ID，
  坐标的最后一个 ID 被删时几何点才消失。
- **共线点策略（固定为保留）**：凸包逆时针输出，边上共线点按顺序包含在链中。
- **退化定义**：空集 `[]`（极值查询抛 `ValueError`）；单点 `[p]`；两点 `[p1, p2]`；
  全共线返回一端到另一端的有序链（含中间点，不往返重复）。
- **来源映射**：`hull_with_ids()` 与 `extreme()` 的返回值都附带每个坐标对应的 ID 元组。

## API 摘要

```python
h = DynamicConvexHull()
h.add("A", 0, 0)                      # 插入（ID 重复抛 ValueError）
h.add("G", Fraction(1, 3), Fraction(1, 3))
h.remove("A")                         # 删除（不存在抛 ValueError）
h.hull()                              # [(Fraction, Fraction), ...] 逆时针链
h.hull_with_ids()                     # [(coord, (ids...)), ...]
h.extreme(dx, dy)                     # (最大值, [(coord, ids), ...])，方向零向量抛错
h.ids_at(x, y)                        # 该坐标当前所有 ID
```

## 复杂度

坐标维护在有序数组中：插入/删除 O(n)，每次变更后单调链 O(n) 重建凸包，
极值查询 O(h)（h 为凸包上点数）。

## 测试结果

```
$ python3.11 -m unittest
.........................
Ran 25 tests in 0.008s
OK
```
