import type { ColoredMultigraph } from "./graph.ts";

/**
 * 1-WL 风格颜色细化（支持平行边与自环）。
 *
 * 每轮用签名 (自身颜色, 有序多重集 {(邻居颜色, 边重数)}) 重新着色；
 * 新颜色 id 按签名的字典序排名分配，因此对同构图（任意顶点编号）
 * 得到的颜色划分完全一致。迭代到划分不再变细为止。
 *
 * 返回长度为 n 的颜色数组，颜色值为 0..k-1 的规范排名。
 */
export function refineColors(g: ColoredMultigraph, initial: number[]): number[] {
  const n = g.vertexCount;
  let colors = normalize(initial);
  let count = new Set(colors).size;
  // 每轮要么划分严格变细（颜色数增加），要么稳定；最多 n 轮。
  for (let iter = 0; iter < n; iter++) {
    const signatures = new Array<string>(n);
    for (let v = 0; v < n; v++) {
      const parts: string[] = [];
      for (let u = 0; u < n; u++) {
        const m = g.adj[v][u];
        if (m > 0) parts.push(colors[u] + "x" + m);
      }
      parts.sort();
      signatures[v] = colors[v] + "|" + parts.join(",");
    }
    const uniq = Array.from(new Set(signatures)).sort();
    const rank = new Map<string, number>();
    for (let i = 0; i < uniq.length; i++) rank.set(uniq[i], i);
    const next = signatures.map((s) => rank.get(s) as number);
    const nextCount = uniq.length;
    colors = next;
    // 新划分必是旧划分的加细；颜色数不变 ⇒ 划分相同 ⇒ 稳定。
    if (nextCount === count) break;
    count = nextCount;
  }
  return colors;
}

/** 把任意整数颜色重映射为按值排序的 0..k-1，保证颜色 id 与输入编号无关。 */
function normalize(colors: number[]): number[] {
  const uniq = Array.from(new Set(colors)).sort((a, b) => a - b);
  const rank = new Map<number, number>();
  for (let i = 0; i < uniq.length; i++) rank.set(uniq[i], i);
  return colors.map((c) => rank.get(c) as number);
}
