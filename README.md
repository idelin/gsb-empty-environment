# pointlocation — 精确多环点定位（Python 3.11，仅标准库）

判断点相对于「一个外环 + 显式声明的若干洞环」的位置：`INSIDE` / `OUTSIDE` / `BOUNDARY`。

## 设计要点

- **精确算术**：坐标只接受 `int` 或 `fractions.Fraction`（`float`/`bool` 直接抛
  `TypeError`）。点在边上、射线交点等全部用有理数精确判定，没有任何 epsilon 修补。
- **显式层次**：洞通过 `Locator(outer, holes=[...])` 显式传入，绝不依赖环方向
  （CW/CCW）猜测；方向不影响结果（有测试保证）。
- **语义**：在任一环的边或顶点上 → `BOUNDARY`；严格在外环内且严格在所有洞外 →
  `INSIDE`；其余（包括洞内部）→ `OUTSIDE`。
- **射线穿顶点不双计**：射线法采用半开规则 `(y1 > py) != (y2 > py)` 加严格
  `x_int > px`：局部极值顶点计 0 或 2 次，单调经过的顶点计 1 次，奇偶性始终正确；
  水平边永不计为交叉，落在其上的点由边界测试捕获。
- **空间索引**：构建一次，服务所有批量查询——
  - 均匀网格（去重后的边 → 覆盖其包围盒的格子）用于边界候选边检索；
  - 每环 slab 索引（边按 min-y 排序 + 二分）用于射线交叉，只检查真正跨越
    射线高度的边。

## 退化情形的定义

- 环规范化：去除连续重复顶点和重复的闭合端点，`[A,B,C,A]` 与 `[A,B,C]` 结果相同。
- 不同顶点数 < 3 的环没有内部，但其顶点/线段仍算边界。
- 零面积（共线）环没有内部，线段算边界。
- 洞应位于外环内；若不满足，结果仍严格遵循「在外环内 ∧ 不在任何洞内」的布尔公式。
- 自交环按 even-odd 规则分类。

## 用法

```python
from fractions import Fraction
from pointlocation import Locator, Location

loc = Locator(
    [(0, 0), (12, 0), (12, 12), (0, 12)],          # 外环
    holes=[[(2, 2), (6, 2), (6, 6), (2, 6)]],       # 显式洞环
)
loc.locate((4, 4))                        # Location.OUTSIDE（洞内部）
loc.locate((2, 4))                        # Location.BOUNDARY（洞边）
loc.locate((Fraction(1, 2), 8))           # Location.INSIDE
loc.locate_many([(1, 1), (20, 20)])       # 批量查询，走空间索引
```

## 运行样例（`python3.11 demo.py` 实际输出节选）

```
Probes
------
  (Fraction(1, 2), Fraction(1, 2)) -> inside
            (6, 8) -> inside
            (4, 4) -> outside      # 方形洞内部
            (9, 9) -> outside      # 三角形洞内部
            (2, 4) -> boundary     # 洞边
            (2, 2) -> boundary     # 洞顶点
           (9, 10) -> boundary     # 三角形斜边（x+y=19，精确命中）
            (0, 7) -> boundary     # 外环边
          (12, 12) -> boundary     # 外环顶点
          (20, 20) -> outside

Map ('#' inside, '.' outside, 'o' boundary), y from 12 down to 0:
  ooooooooooooooooooooooooo
  o#######################o
  o###############o#######o
  o###############oo######o
  o###############o.o#####o
  o###############o..o####o
  o###############o...o###o
  o###############o....o##o
  o###############ooooooo#o
  o#######################o
  o#######################o
  o#######################o
  o###ooooooooo###########o
  o###o.......o###########o
  o###o.......o###########o
  o###o.......o###########o
  o###o.......o###########o
  o###o.......o###########o
  o###o.......o###########o
  o###o.......o###########o
  o###ooooooooo###########o
  o#######################o
  o#######################o
  o#######################o
  ooooooooooooooooooooooooo

Batch: 1089 points classified in 600.6 ms via spatial index -> inside=420, outside=523, boundary=146
```

## 测试结果（`python3.11 -m unittest test_pointlocation -v`）

```
Ran 29 tests in 5.467s

OK
```

覆盖：基本内外/边界、洞语义（洞内=外部、洞边/洞顶点=边界）、多洞、洞在外环外、
射线穿过凹/凸顶点不双计、水平边、重复闭合端点与连续重复点规范化、少于 3 点的
退化环、零面积共线环、退化洞仍产生边界、分数坐标精确性（1/3、10^-12 级扰动）、
拒绝 float/bool、批量与逐点一致性、2000 个随机有理点与无索引暴力实现交叉验证。

## 文件

- `pointlocation.py` — 实现（`Locator`、`Location`、规范化与两个空间索引）
- `test_pointlocation.py` — unittest 套件（29 个测试）
- `demo.py` — 运行样例（探针、ASCII 地图、批量计时）
