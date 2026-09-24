# Exact Multi-Ring Point Location

Python 3.11 标准库实现。坐标只接受 `int` 或 `fractions.Fraction`，所有方向、面积、交点判断都使用精确有理运算，不使用 epsilon，也不根据顺逆时针猜测洞。

## 模型

- `RingNode(points, children)` 表示一个环及其显式子环。
- 根环是外环；其子环是洞；洞的子环是岛，依此类推。
- 查询结果为 `Location.INSIDE`、`Location.OUTSIDE` 或 `Location.BOUNDARY`。
- 洞内部是外部；洞的边是边界。
- 构造时拒绝零面积环、自交环、重复非相邻顶点、环边接触/交叉，以及几何包含关系与显式层次不一致的输入。
- 重复闭合端点和相邻重复点会被规范化；共线但不重叠的额外观测点允许保留。

## 运行

```bash
python3.11 demo.py
python3.11 -m unittest -v
```

## 示例

```python
from fractions import Fraction
from point_location import PolygonSet, RingNode

geometry = PolygonSet([
    RingNode(
        [(0, 0), (10, 0), (10, 10), (0, 10)],
        [RingNode(
            [(2, 2), (8, 2), (8, 8), (2, 8)],
            [RingNode([(4, 4), (6, 4), (6, 6), (4, 6)])],
        )],
    )
])

geometry.locate((1, 1))                    # INSIDE
geometry.locate((3, 5))                    # OUTSIDE（洞内）
geometry.locate((5, 5))                    # INSIDE（岛内）
geometry.locate((Fraction(1, 2), 0))       # BOUNDARY
geometry.locate_all([(1, 1), (3, 5)])
```

批量查询通过静态 STR R-tree 裁剪候选边；R-tree 只做包围盒过滤，分类仍由精确拓扑谓词完成。
