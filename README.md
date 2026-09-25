# polytri — 含洞简单多边形的精确三角剖分

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数运算），
对含洞简单多边形做三角剖分，输出每个三角形的**来源（provenance）**，
并附带独立的**面积守恒验证器**。全程无浮点、无 epsilon：
所有拓扑判断（共线、点在线上、点在环内、线段相交）都是精确符号判定。

## 功能

- 外环 + 任意数量互不相交的洞 → 三角形列表。
- 每个三角形输出来源标签：`outer`、`hole[k]` 或混合（如 `hole[0]+outer`，即跨桥三角形）。
- 合法洞桥：桥开线段不触碰任何边界（共享端点除外），精确判定。
- 共线顶点、连续重复顶点在预处理阶段被精确移除（不改变区域）。
- 验证器检查：非退化（无零面积）、顶点来自输入、三角形不越界不入洞、
  两两内部不相交、面积和 == 外环面积 − 洞面积和（精确相等）。

## 退化情形的定义

| 情形 | 处理 |
|---|---|
| 连续重复顶点、落在邻边线段上的共线顶点 | 预处理精确移除（区域不变） |
| 自交/自触、尖刺（spike）、零面积环 | 抛 `PolygonError` |
| 洞在外环外、洞贴外环、洞相交/相切/嵌套、零面积洞 | 抛 `PolygonError` |
| 桥射线恰好命中顶点、桥与边共线 | 由精确可见性测试自然处理，不靠 epsilon |
| 桥产生的重复顶点（slit） | 只允许出现在零宽缝上；输出三角形面积严格为正，重复顶点不会形成零面积输出 |
| 找不到合法桥或耳 | 抛 `TriangulationError` |

## 算法

1. **规范化**（`polytri/preprocess.py`）：清理冗余顶点，校验简单性，
   统一朝向（外环 CCW、洞 CW），校验洞严格在外环内且互不相交。
2. **洞桥**（`polytri/triangulate.py`）：按"最右洞顶点优先、距离最近优先"
   的确定顺序搜索桥 `(h, w)`，要求开线段与所有边界边无任何接触
   （共享端点除外）。把洞按 CW 方向拼接进外环，桥两端点各复制一次，
   形成带零宽缝的弱简单环。
3. **耳切**：耳必须严格凸、不含其他顶点（含边界，桥复制点豁免）、
   对角线不与任何边正常相交、对角线中点严格在环内。
   只输出方向严格为正的三角形。
4. **验证**（`polytri/validate.py`）：从输入环和输出三角形独立重算
   全部五条检查，给出 `ValidationReport`。

## 用法

```python
from polytri import triangulate, validate_triangulation

outer = [(0, 0), (10, 0), (10, 10), (0, 10)]
holes = [[(3, 3), (7, 3), (7, 7), (3, 7)]]

triangles = triangulate(outer, holes)
for t in triangles:
    print(t.vertices, t.area, t.source_label)

report = validate_triangulation(outer, holes, triangles)
assert report.ok
print(report.summary())
```

坐标接受 `int` 或 `fractions.Fraction`（浮点数会被拒绝，以免引入误差）。

## 运行

```bash
python3.11 examples/demo.py          # 运行样例（5 个例子）
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 样例输出（节选）

```
Example 1: 10x10 square with an axis-aligned square hole
8 triangles:
  T00  area=    15  src=hole[0]+outer       [(10, 0), (10, 10), (7, 7)]
  ...
validation:
  [PASS] input_valid
  [PASS] non_degenerate
  [PASS] vertices_from_input
  [PASS] inside_region
  [PASS] triangles_disjoint
  [PASS] area_conservation
  triangle area sum : 84
  region area       : 84
  overall           : OK
```

## 测试结果

```
Ran 43 tests in 3.0s

OK
```

覆盖：精确谓词（共线/在线段上/点在环内/线段关系）、环清理与非法输入
（自交、尖刺、零面积、洞越界/相切/嵌套）、正方形+方洞、有理数坐标、
双环共线顶点、桥射线恰中顶点、凹外环双洞、桥须绕开其他洞、输出确定性、
输入朝向无关性、三角形计数公式 `V + 2H − 2`，以及验证器对
缺三角形、零面积三角形、洞内三角形、外来顶点、重叠三角形的检出。

## 目录结构

```
polytri/
  exact.py        # 精确几何谓词（Fraction，无 epsilon）
  preprocess.py   # 输入清理与合法性检查
  triangulate.py  # 洞桥构造 + 耳切三角剖分 + Triangle 类型
  validate.py     # 面积守恒与合法性验证器
examples/demo.py  # 运行样例
tests/            # unittest 测试（43 个）
```
