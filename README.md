# rect_union

对轴对齐矩形集合并集计算**精确**面积、周长与外轮廓（Python 3.11，仅标准库）。

所有计算基于 `fractions.Fraction` 的精确有理数运算，**不使用任何浮点数或
epsilon 容差**：内外判定、顶点配对等拓扑判断全部由精确有理数比较得出。

## 功能与约定

- 坐标为整数或有理数（`int` / `Fraction` / 数值字符串如 `"1/3"`）。
  `float` 会被拒绝（抛出 `TypeError`），以免二进制舍入误差混入精确计算。
- 矩形为闭集 `[x1, x2] x [y1, y2]`，参数顺序自动规范化；也接受
  `(x1, y1, x2, y2)` 元组。
- **退化（零面积）矩形**（`x1 == x2` 或 `y1 == y2`，即线段或点）被完全忽略：
  不贡献面积、周长与轮廓。
- 支持任意**重叠、包含、边接触**；边接触合并为单个环。
- **角接触**（仅一点相接）产生两个独立的环，二者共享该顶点，
  绝不拼成自交的 8 字环。孔洞与外轮廓仅一点相接时同样拆分为两个简单环。
- 环方向规范：外环逆时针（CCW），孔环顺时针（CW）；每个环以字典序最小
  顶点起始，输出确定；共线顶点已化简；闭合点不重复存储。

## 用法

```python
from rect_union import Rectangle, union, union_area, union_perimeter, union_contours

result = union([Rectangle(0, 0, 1, 1), Rectangle(1, 0, 2, 1)])
result.area        # Fraction(2) —— 两个相邻单位方块
result.perimeter   # Fraction(6)
result.polygons    # (Polygon(outer=((0,0),(2,0),(2,1),(0,1)), holes=()),)
```

`Polygon.area` / `Polygon.perimeter` 给出单个连通分量（含孔洞）的精确值。

运行样例：

```
python3.11 demo.py
```

运行测试（21 个用例，含与独立扫描线算法的随机对拍）：

```
python3.11 -m unittest discover -s tests -t . -v
```

## 算法

1. 收集所有矩形边坐标，切出基本网格；逐单元精确判定是否被任一矩形覆盖。
2. 面积 = 被覆盖单元面积之和；周长 = 单元边界中"一侧填充一侧空"的边长之和。
3. 轮廓：发射"内部在左"的有向边界边，缝合为闭环。顶点处出入度相等，
   缝合必可完成；对角接触（棋盘格构型）在局部无法区分"角接触"与
   "孔洞掐点"，因此缝合后对经过重复顶点的闭行走做拆分，保证每个环简单。
4. 按鞋带公式符号分类外环（正）与孔环（负），孔归入包含它的最小外环。

复杂度：设不同 x、y 坐标数为 X、Y，则填充与边界提取为 O(X·Y)，
缝合与环数成线性关系；适合精确性优先的场景。

## 文件

- `rect_union.py` —— 库（`Rectangle` / `union` / `union_area` /
  `union_perimeter` / `union_contours` / `Polygon` / `UnionResult`）
- `demo.py` —— 可运行样例
- `tests/test_rect_union.py` —— unittest 测试
