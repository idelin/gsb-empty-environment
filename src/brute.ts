// 暴力参考解：用于核验精确优化器。
// 从理论下界开始枚举批次数 k，回溯判断是否存在可行分配，返回最小的 k。
// 仅适用于小规模（分量数 <= 10 左右）。

export function bruteForceMinBatches(sizes: number[], predMask: number[], capacity: number): number | null {
  const n = sizes.length;
  for (const s of sizes) {
    if (s > capacity) return null; // 依赖环比一批的容量还大：不可行
  }
  let total = 0;
  for (const s of sizes) total += s;

  // 凝聚 DAG 的拓扑序（前驱先排）
  const order: number[] = [];
  const placed: boolean[] = [];
  for (let i = 0; i < n; i++) placed.push(false);
  for (let t = 0; t < n; t++) {
    for (let i = 0; i < n; i++) {
      if (placed[i]) continue;
      let ready = true;
      for (let j = 0; j < n; j++) {
        if ((predMask[i] & (1 << j)) !== 0 && !placed[j]) { ready = false; break; }
      }
      if (ready) { order.push(i); placed[i] = true; break; }
    }
  }

  // 最长链下界
  const succ: number[][] = [];
  for (let i = 0; i < n; i++) succ.push([]);
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      if ((predMask[i] & (1 << j)) !== 0) succ[j].push(i);
    }
  }
  const height = new Array<number>(n).fill(0);
  const heightOf = (u: number): number => {
    if (height[u] > 0) return height[u];
    let best = 1;
    for (const v of succ[u]) best = Math.max(best, 1 + heightOf(v));
    height[u] = best;
    return best;
  };
  let maxH = 0;
  for (let i = 0; i < n; i++) maxH = Math.max(maxH, heightOf(i));

  const feasible = (k: number): boolean => {
    const used = new Array<number>(k).fill(0);
    const assign = new Array<number>(n).fill(-1);
    const rec = (idx: number): boolean => {
      if (idx === n) return true;
      const c = order[idx];
      let minB = 0;
      let hasPred = false;
      for (let j = 0; j < n; j++) {
        if ((predMask[c] & (1 << j)) !== 0) {
          hasPred = true;
          if (assign[j] >= minB) minB = assign[j];
        }
      }
      if (hasPred) minB += 1; // 严格语义：前驱必须在更早批次
      for (let b = minB; b < k; b++) {
        if (used[b] + sizes[c] > capacity) continue;
        used[b] += sizes[c];
        assign[c] = b;
        if (rec(idx + 1)) return true;
        used[b] -= sizes[c];
        assign[c] = -1;
      }
      return false;
    };
    return rec(0);
  };

  const lb = Math.max(Math.ceil(total / capacity), maxH);
  for (let k = lb; k <= n; k++) {
    if (feasible(k)) return k;
  }
  return null; // 不会到达：k = n 时每个分量独占一批必然可行
}
