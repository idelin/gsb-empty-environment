# 带节点颜色的多重图同构检测器

TypeScript 实现。支持平行边与自环，顶点输入顺序不影响结论。

## 模型

```ts
interface ColoredMultigraph {
  vertexCount: number;
  colors: number[];   // 每个顶点的颜色
  adj: number[][];    // adj[u][v] = 平行边条数（对称），adj[v][v] = 自环条数
}
```

## 算法

1. **颜色细化**（`src/refine.ts`）：1-WL 风格迭代，签名为
   `(自身颜色, 有序多重集 {(邻居颜色, 边重数)})`，新颜色按签名字典序排名分配，
   因此颜色划分与顶点编号无关。
2. **个体化-细化规范形**（`src/canonical.ts`）：细化后若染色离散则输出证书；
   否则选取颜色 id 最小的非平凡单元（该选择同构不变），逐个顶点个体化并递归，
   取字典序最大证书。证书 = 规范顺序下的 `(n, 原始颜色序列, 邻接上三角)`，
   **任意重标号后完全相同**；两图同构当且仅当证书相同。
3. **判定**（`src/isomorphic.ts`）：`checkIsomorphism(a, b, budget)` 返回三态：
   - `isomorphic`：附顶点映射 `mapping`，可用 `verifyIsomorphism` 独立核对；
   - `non-isomorphic`：附双方规范编码（或平凡结构差异原因），可用
     `verifyIsoResult` 重新计算核对；
   - `unknown`：个体化次数超出 `budget`，搜索耗尽，不下结论。

注意：颜色直方图相同**绝不**作为同构证据（仅直方图不同用于快速排除）。
细化后仍不可区分的图（如 C6 vs 2×C3、三棱柱 vs K3,3、Shrikhande vs 4×4 车图）
一律进入搜索，由规范编码定夺。

## 运行

无第三方依赖。Node ≥ 23.6 直接运行 TypeScript；Node 22 需加标志。

```sh
npm test            # node --test "tests/*.test.ts"
npm run test:node22 # node --experimental-strip-types ...
npm run typecheck   # 需要本机安装 typescript
```

## 测试要点（`tests/iso.test.ts`）

- 随机带色多重图（含平行边、自环）**全部 5! 排列**：规范编码逐一同等；
- Petersen、K6、C9、Q3、Shrikhande、车图等**高对称图**随机重标号：编码不变；
- 300 组随机小图与**暴力枚举全部双射**的地面真值逐一比对，映射可核对；
- WL 不可区分但不同构的图对（颜色直方图相同）必须判 `non-isomorphic`；
- 平行边/自环重数、节点颜色均参与判定；
- 预算为 0 时 Petersen 自比较返回 `unknown`，且耗尽事实可核对。
