# 多维空间有理数超平面分离定理核验器（2D）

两组平面凸点集的**严格线性可分性验证器**。全程使用 BigInt 精确有理数运算，
无任何浮点误差：

- **可分离**：输出精确有理数系数的分离超平面（最大间隔超平面，即两凸包最近
  点对连线的垂直平分线），形如 `2x - 3y + 5 = 0`，两组点严格落在两侧。
- **不可分离**：输出两凸包交集（重叠区域）的顶点及其**重心点**，并给出该点
  在两个点集中的精确重心坐标（凸组合）证书，证明两凸包相交。

## 运行环境

- Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译、无第三方依赖）
- 推荐 v23.9.0（仓库附 `.nvmrc`）

## 使用方法

```bash
node src/cli.ts examples/separated.json        # 从文件读取
cat examples/intersecting.json | node src/cli.ts -   # 从标准输入读取
npm test                                       # Node.js 内置测试运行器
npm run demo                                   # 运行全部示例
```

输入 JSON 格式（坐标支持整数、小数、分数字符串）：

```json
{
  "A": [[0, 0], [2, 0], [0, 2]],
  "B": [["9/2", 0], [6, 0], ["4.5", 2]]
}
```

## 算法

1. **凸包**：单调链法（Andrew monotone chain），精确叉积符号判断，去除共线点；
   支持退化情形（单点、线段）。
2. **交集判定**：两凸多边形交集顶点 = 「A 顶点在 B 内」∪「B 顶点在 A 内」
   ∪「边-边交点」，再取凸包。紧致凸集不相交 ⟺ 严格可分。
3. **可分离**：枚举两凸包边对求最近点对（Ericson 线段-线段最近点算法，参数
   全部有理数），最近点对连线的垂直平分线即最大间隔分离超平面；系数通分为
   互素整数后输出 `ax + by + c = 0`。
4. **不可分离**：输出重叠区域顶点的算术平均（重心点，严格落在重叠区域内部），
   并用 Carathéodory 分解（2D 至多 3 个顶点）给出该点在每个点集中的精确
   凸组合证书。

## 运行样例

```
=== examples/separated.json ===
verdict: SEPARABLE
hyperplane: x - 3 = 0
coefficients: a=-2, b=0, c=6
closest point in conv(A): (2, 0)
closest point in conv(B): (4, 0)
strict margins: min_A h = 2 > 0, max_B h = -2 < 0

=== examples/rational.json ===  (A 含分数坐标 1/2)
verdict: SEPARABLE
hyperplane: 4x + 4y - 3 = 0
strict margins: min_A h = 1/16 > 0, max_B h = -1/16 < 0

=== examples/intersecting.json ===
verdict: NOT SEPARABLE (convex hulls intersect)
overlap region vertices: (2, 1) (3, 1) (2, 2)
common point: (7/3, 4/3)
in conv(A): 1/12*(0, 0) + 7/12*(4, 0) + 1/3*(0, 4)
in conv(B): 5/6*(2, 1) + 1/12*(6, 1) + 1/12*(2, 5)
```

## 测试结果

`node --test test/*.test.ts`（Node v23.9.0）：**22 个测试全部通过**，覆盖：

- 有理数四则运算 / 约分 / 小数与科学计数法精确解析 / 比较；
- 凸包（含共线点、退化点与线段）、线段最近点对、点在凸包内判定；
- 可分离：轴对齐 / 对角 / 分数坐标分离，超平面严格性验证；
- 不可分离：重叠三角形（重叠区域顶点 + 重心点 + 双边凸组合证书）、
  共享顶点相切、包含关系、点与线段退化情形；
- 200 组随机点集自检：超平面严格分离性或重心证书必然验证通过。

```
ℹ tests 22
ℹ pass 22
ℹ fail 0
```

## 项目结构

```
src/rational.ts   精确有理数（BigInt 分子分母，自动约分）
src/geometry.ts   凸包、线段最近点对、凸包交集、Carathéodory 重心分解
src/separate.ts   可分性判定主逻辑 + 超平面格式化
src/cli.ts        命令行入口
test/             Node.js 内置测试（node:test + node:assert）
examples/         示例输入
```
