// 精确优化器：在依赖先后约束与每批容量约束下，最小化批次数。
//
// 语义（严格先后 + 环同批）：
//   - 若项目 A 依赖 B 且二者不在同一依赖环，则 B 必须在严格更早的批次；
//   - 同一强连通分量（依赖环）内的项目必须同批发布。
// 分量视角：m 个强连通分量（m <= 18），分量 c 占 sizes[c] 个容量，
// predMask[c] 为必须严格早于 c 的分量集合（位掩码）。
//
// 算法：对「已完成集合 done」（位掩码）做记忆化搜索。每步从「可安排集」
// （前驱全部完成的分量）中枚举一个极大的容量可行子集作为下一批。
// 支配规则：若 S 真包含于可行子集 S'，则选 S' 不会更差——已完成集合越大，
// 后续可安排集越大，而批次数相同。故只需枚举极大子集。
// 剪枝：容量下界 ceil(剩余大小/容量) 与最长链下界；达到下界即提前终止。
// 状态数 <= 2^m，m <= 18。

export function minimizeBatches(sizes: number[], predMask: number[], capacity: number): number[][] {
  const m = sizes.length;
  const full = (1 << m) - 1;
  const bit: number[] = [];
  for (let i = 0; i < m; i++) bit.push(1 << i);
  const log2 = new Uint8Array(1 << m);
  for (let i = 0; i < m; i++) log2[1 << i] = i;

  // sizeSum[mask]：掩码内分量占用的项目总数
  const sizeSum = new Int32Array(1 << m);
  for (let mask = 1; mask <= full; mask++) {
    const lsb = mask & -mask;
    sizeSum[mask] = sizeSum[mask ^ lsb] + sizes[log2[lsb]];
  }

  // 凝聚 DAG 上的后继与最长链（链下界用）
  const succ: number[][] = [];
  for (let i = 0; i < m; i++) succ.push([]);
  for (let i = 0; i < m; i++) {
    for (let j = 0; j < m; j++) {
      if ((predMask[i] & bit[j]) !== 0) succ[j].push(i);
    }
  }
  const height = new Array<number>(m).fill(0);
  const heightOf = (u: number): number => {
    if (height[u] > 0) return height[u];
    let best = 1;
    for (const v of succ[u]) best = Math.max(best, 1 + heightOf(v));
    height[u] = best;
    return best;
  };
  for (let i = 0; i < m; i++) heightOf(i);

  const memo = new Int16Array(1 << m).fill(-1);
  const choice = new Int32Array(1 << m).fill(-1);

  const solve = (done: number): number => {
    if (done === full) return 0;
    const cached = memo[done];
    if (cached >= 0) return cached;

    let avail = 0;
    for (let i = 0; i < m; i++) {
      if ((done & bit[i]) === 0 && (predMask[i] & ~done) === 0) avail |= bit[i];
    }
    const remSize = sizeSum[full] - sizeSum[done];
    let maxHeight = 0;
    for (let i = 0; i < m; i++) {
      if ((done & bit[i]) === 0 && height[i] > maxHeight) maxHeight = height[i];
    }
    const lb = Math.max(Math.ceil(remSize / capacity), maxHeight);

    const items: number[] = [];
    for (let i = 0; i < m; i++) {
      if ((avail & bit[i]) !== 0) items.push(i);
    }

    let best = m + 1;
    let bestSub = -1;
    let stop = false;

    // 枚举极大可行子集：include/exclude 递归；minFitExcl 记录「被排除时
    // 仍能放下」的最小分量尺寸，叶子处若它 <= 剩余容量则该子集非极大。
    const rec = (idx: number, rem: number, acc: number, minFitExcl: number): void => {
      if (stop) return;
      if (idx === items.length) {
        if (acc === 0) return;
        if (minFitExcl <= rem) return; // 非极大，被某个超集支配
        const nd = done | acc;
        // 候选下界剪枝
        const rSize = sizeSum[full] - sizeSum[nd];
        let lbh = 0;
        for (let i = 0; i < m; i++) {
          if ((nd & bit[i]) === 0 && height[i] > lbh) lbh = height[i];
        }
        const candLb = 1 + Math.max(Math.ceil(rSize / capacity), lbh);
        if (candLb >= best) return;
        const r = 1 + solve(nd);
        if (r < best) {
          best = r;
          bestSub = acc;
          if (best === lb) stop = true; // 已达理论下界
        }
        return;
      }
      const c = items[idx];
      const s = sizes[c];
      if (s <= rem) {
        rec(idx + 1, rem - s, acc | bit[c], minFitExcl);
        rec(idx + 1, rem, acc, Math.min(minFitExcl, s));
      } else {
        rec(idx + 1, rem, acc, minFitExcl);
      }
    };
    rec(0, capacity, 0, Infinity);

    memo[done] = best;
    choice[done] = bestSub;
    return best;
  };

  solve(0);

  const batches: number[][] = [];
  let done = 0;
  while (done !== full) {
    const sub = choice[done];
    const batchList: number[] = [];
    for (let i = 0; i < m; i++) {
      if ((sub & bit[i]) !== 0) batchList.push(i);
    }
    batches.push(batchList);
    done |= sub;
  }
  return batches;
}
