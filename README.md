# colored-graph-isomorphism

带顶点颜色的无向**多重图**（允许平行边、自环）同构检测器，TypeScript 实现，零依赖。

## 算法

1. **颜色细化**（1-WL）：初始着色来自顶点颜色；反复按
   `(自身颜色, 排序后的 [邻居颜色, 边重数] 序列)` 分裂颜色类直到稳定。
   边重数参与签名，因此平行边结构被精确区分。
2. **个体化-细化搜索（IR）**：取第一个非平凡格，逐个顶点个体化并再细化，
   递归到离散划分（叶子）。叶子给出顶点的规范序。
3. **自同构轨道剪枝**：与当前最优叶证书相同的叶合成自同构，向上传播，
   在各层目标格上做并查集轨道剪枝，高对称图（K_n、Petersen、强正则图）不会组合爆炸。
4. **规范编码**：所有叶子证书（规范序下的颜色序列 + 上三角重数矩阵）取字典序最小，
   序列化为字符串。**同一抽象图的任意顶点重标号得到完全相同的编码**。

## 结果三态

- `isomorphic`：附 `mapping`（g1→g2），可用 `verifyIsomorphism` 独立核对；
- `not-isomorphic`：附双方规范编码 `canonical1`/`canonical2`，可用 `canonicalForm` 复算核对；
- `unknown`：搜索预算（树节点数）耗尽。预算只限制搜索规模，**绝不**为省预算而猜测；
  颜色直方图等必要条件只用于否决，绝不用于肯定。

## API

```ts
import { createGraph, relabel, isomorphic, canonicalForm, verifyIsomorphism } from "./src/graphiso.ts";

const g1 = createGraph(4, [0, 0, 1, 1], [[0, 1], [0, 1], [1, 2], [2, 3], [3, 0]]); // 平行边
const g2 = relabel(g1, [2, 0, 3, 1]);                                             // 任意重标号
const r = isomorphic(g1, g2, /* budget */ 1_000_000);
// r.status === "isomorphic"，verifyIsomorphism(g1, g2, r.mapping) === true
canonicalForm(g1).canonical === canonicalForm(g2).canonical; // 恒等
```

## 运行

需要 Node ≥ 22.18（或 ≥ 23.6，内置 TypeScript 类型擦除，无需编译）；或自行用 `tsc` 编译。

```sh
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 测试要点（test/graphiso.test.ts）

- **小图全排列**：K4/K5/C5/C6/K3,3/K2,3/三棱柱/多重边图/带环图/着色路径，
  对全部 n! 种顶点排列断言：判同构、映射通过 `verifyIsomorphism`、规范编码逐字符相同。
- **高对称图随机重标号**：Petersen、超立方 Q3、Shrikhande、4×4 车图。
- **直方图陷阱**：Shrikhande 与 L2(4) 车图同为 (16,6,2,2) 强正则、颜色直方图相同、
  1-WL 不可区分，必须判 `not-isomorphic`；着色直方图相同但结构不同亦然。
- **预算耗尽**：Petersen 在 budget=1 下返回 `unknown`，预算充足时判 `isomorphic`。
- **核对器**：拒绝非双射、破坏颜色、破坏边的映射。
