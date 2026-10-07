# Triangulation Checker (C++17)

验证一组候选三角形是否**无重叠、无空洞地精确覆盖**一个简单多边形。

- 顶点为有理数（支持 `整数`、`p/q`、`小数` 三种输入），全程精确有理数运算
  （自带任意精度整数，无第三方依赖）。
- 多边形允许共线边；三角形允许使用新增内部点（Steiner 点）和 T 型接头。
- **面积相等不是充分条件**：本检查器除面积外还验证边界覆盖、内部边配对与边的交叉。

## 构建与测试

```sh
make            # 生成 ./tri_checker
make test       # 运行 tests/ 下全部用例
```

## 输入格式

```
N
x1 y1
...          (N 行，多边形顶点，顺序排列)
M
ax ay bx by cx cy
...          (M 行，每行一个三角形)
```

从 stdin 读取，或 `tri_checker <file>`。输出 `VALID`，或 `INVALID` 加具体证据
（哪条边交叉、哪段边界未覆盖/重复覆盖、哪段内部边未配对、面积各是多少）。
退出码：0 合法，1 非法，2 输入错误。

## 检查管线

1. **多边形合法性**：非零面积、零长边、非相邻边相交（自交）、相邻边回退重叠。
2. **退化三角形**：零面积三角形直接报告并排除。
3. **交叉检查**：三角形边两两之间、三角形边与多边形边之间的真交叉
   （跨立实验，精确符号判定）。端点相接、共线重叠（T 型接头）合法。
4. **边界覆盖**：把所有边按支撑线（规范化整数方向 + 偏移）分组，在每条线上做
   一维扫描：落在多边形边界上的基本区间必须被三角形边覆盖**恰好 1 次**。
5. **内部边配对**：不在边界上的基本区间必须被覆盖**恰好 2 次**
   （两侧各一个三角形）。覆盖 1 次 = 空洞或悬空边，>2 次 = 重叠。
6. **面积相等**：三角形面积和 == 多边形面积（精确比较）。

为什么 4–6 缺一不可：

- 只有面积：存在面积和相等但「重叠一块 + 漏掉等面积一块」的伪剖分
  （见 `tests/invalid_equal_area_overlap_hole.txt`，以及更隐蔽的
  `tests/invalid_equal_area_silent.txt`——无交叉、面积相等，仅靠内部边配对抓获）。
- 只有配对：可以构造「折叠」双重覆盖，每条边都配了对但整体多盖一层
  （`tests/invalid_folded.txt`），此时面积和是多边形面积的 2 倍，由第 6 步抓获。

## 证据示例

```
INVALID
  unpaired interior edge segment (0,0)--(1,1) covered by 1 triangle edge, expected 2 (hole or dangling edge)
  uncovered boundary segment (0,1)--(0,2) (hole reaching the polygon boundary)
  crossing edges: triangle 0 edge (2,0)--(0,2) crosses triangle 1 edge (1,0)--(2,2)
  area mismatch: sum of triangle areas = 7/2, polygon area = 4
```

## 文件

- `src/exact.h` — 任意精度整数 + 有理数（约分、四则、比较、解析）
- `src/geom.h` — 精确几何：方向判定、线段相交、支撑线规范化
- `src/tri_checker.cpp` — 检查管线与 CLI
- `tests/` — 正/反用例，`run_tests.sh` 驱动
