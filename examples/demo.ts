import { createGraph, relabel, isomorphic, canonicalForm, verifyIsomorphism } from "../src/graphiso.ts";

// 带颜色与平行边的小图
const g1 = createGraph(4, [0, 0, 1, 1], [
  [0, 1], [0, 1], // 平行边
  [1, 2], [2, 3], [3, 0],
]);
// 同一抽象图的另一种顶点编号
const g2 = relabel(g1, [2, 0, 3, 1]);

const r = isomorphic(g1, g2);
console.log("status:", r.status);
if (r.status === "isomorphic") {
  console.log("mapping g1->g2:", r.mapping);
  console.log("mapping verifies:", verifyIsomorphism(g1, g2, r.mapping));
  console.log("canonical:", r.canonical);
}

// 规范编码与顶点输入顺序无关
console.log("canonical equal:", canonicalForm(g1).canonical === canonicalForm(g2).canonical);

// 预算耗尽 -> unknown（不是误判）
console.log("tiny budget:", isomorphic(g1, g2, 1).status);
