# polygon_clipper

用凸裁剪窗裁剪简单多边形（Python 3.11+，仅标准库，测试用 `unittest`）。

所有计算基于 `fractions.Fraction` 精确有理数运算：方向、点在线段上、线段相交等
拓扑判断全部是精确谓词，**不使用任何 epsilon 容差**。

## 规则定义

- **闭集语义（边界包含规则统一）**：主题多边形与裁剪窗都视为闭集，边界属于区域。
  裁剪窗是其所有边的闭半平面之交，因此恰好贴在裁剪边上的边界不会丢失。
- **坐标**：接受 `int`、`Fraction` 或有理数字符串（如 `"1/3"`、`"2.5"`），
  内部统一为 `Fraction`；`float` 会被拒绝（`TypeError`），避免隐式二进制误差。
- **主题多边形必须简单**：≥3 个互异顶点；非相邻边不得相交或相触（顶点不得落在
  非相邻边上）；相邻边不得回折重叠。违反抛 `SelfIntersectionError`。
  零面积或顶点不足抛 `DegenerateInputError`。允许共线的直行连续边。
- **裁剪窗必须为凸**：简单、非零面积的凸多边形，否则抛 `NonConvexWindowError`。
  两个操作数的顺/逆时针方向均可。
- **退化输出**：裁剪结果按连通区域输出。正面积区域 `kind="polygon"`；
  当（部分）交集塌缩为线段或单点时，输出 `kind="line"` / `kind="point"` 的
  退化区域（`Region.degenerate == True`，面积为 0）。仅连接两个正面积分量的
  零宽"桥"线段被吸收入相邻分量，不单独报告。
- **来源边信息**：每条输出边记录其落在哪些主题边 / 窗口边上（边索引，
  见 `EdgeSource.subject_edges` / `EdgeSource.window_edges`）；点区域记录其落在
  哪些输入边上（`on_subject_edges` / `on_window_edges`）。
- **不变量**：输出总面积 ≤ 主题面积；对输出区域再次裁剪结果不变（幂等）。
  两者均由测试精确验证。

## 用法

```python
from polygon_clipper import clip_polygon

result = clip_polygon(
    subject=[(0, 0), (4, 0), (4, 4), (3, 4), (3, 1), (1, 1), (1, 4), (0, 4)],
    window=[(-1, 2), (5, 2), (5, 5), (-1, 5)],
)
for region in result.regions:
    print(region.kind, region.vertices, region.area)
    for edge in region.edges:
        print(edge.a, "->", edge.b, edge.subject_edges, edge.window_edges)
```

## 运行

```sh
python3 demo.py          # 运行样例
python3 -m unittest -v   # 运行测试
```

## 实现要点

1. 输入校验：精确 O(n²) 简单性检查（含相邻边回折、T 型相触）与凸性检查。
2. Sutherland–Hodgman 按窗口边逐次做闭半平面裁剪，交点用 `Fraction` 精确表示。
3. 裁剪结果是弱简单环：先在其边内部的顶点处剖分，再沿"同段反向"的零宽桥边对
   递归拆分成若干简单环。
4. 每个环分类为 polygon / line / point，并对每条输出边回溯其来源输入边。
