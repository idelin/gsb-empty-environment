// 末行保留集求解:给定剩余原子宽度、关键片段标记、行宽与省略号宽,
// 求"保留哪些原子 + 在空隙放省略号"的可行方案,最大化保留目标:
//   主目标 = 保留的关键原子数;次目标 = 保留的总原子数。
// 小规模用穷举(对照最优),大规模用贪心 + 交换改进。

export interface KeepSetInput {
  widths: number[];
  /** 原子是否落在任一关键片段内 */
  keyAtoms: boolean[];
  lineWidth: number;
  ellipsisWidth: number;
}

/** 一个保留集的视觉代价:保留宽度 + 每个省略空隙一个省略号。 */
export function costOf(kept: boolean[], widths: number[], ellipsisWidth: number): number {
  const n = kept.length;
  let keptWidth = 0;
  let groups = 0;
  let first = -1;
  let last = -1;
  let prev = false;
  for (let i = 0; i < n; i++) {
    if (kept[i]) {
      keptWidth += widths[i];
      if (!prev) groups++;
      if (first < 0) first = i;
      last = i;
    }
    prev = kept[i];
  }
  if (groups === 0) return ellipsisWidth; // 全部省略:一个省略号
  let ellipses = groups - 1;
  if (first > 0) ellipses++;        // 头部有省略
  if (last < n - 1) ellipses++;     // 尾部有省略
  return keptWidth + ellipses * ellipsisWidth;
}

export function scoreOf(kept: boolean[], keyAtoms: boolean[]): number {
  let keys = 0;
  let count = 0;
  for (let i = 0; i < kept.length; i++) {
    if (kept[i]) {
      count++;
      if (keyAtoms[i]) keys++;
    }
  }
  return keys * 1_000_000 + count;
}

export interface KeepSetResult {
  kept: boolean[];
  feasible: boolean;
  cost: number;
  score: number;
}

/** 穷举全部 2^n 保留集,返回最优可行解;n 较大时调用方应改用贪心。 */
export function exhaustiveKeepSet(input: KeepSetInput): KeepSetResult {
  const n = input.widths.length;
  if (n > 24) throw new Error('exhaustiveKeepSet: n too large');
  let best: KeepSetResult | null = null;
  const kept = new Array<boolean>(n).fill(false);
  const total = 1 << n;
  for (let mask = 0; mask < total; mask++) {
    for (let i = 0; i < n; i++) kept[i] = (mask & (1 << i)) !== 0;
    const cost = costOf(kept, input.widths, input.ellipsisWidth);
    if (cost > input.lineWidth) continue;
    const score = scoreOf(kept, input.keyAtoms);
    if (!best || score > best.score) best = { kept: kept.slice(), feasible: true, cost, score };
  }
  if (best) return best;
  // 无可行解(连全省略都放不下):返回全省略,标记不可行
  return {
    kept: new Array<boolean>(n).fill(false),
    feasible: false,
    cost: input.ellipsisWidth,
    score: 0,
  };
}

/** 在 kept 基础上反复加入可行原子(代价最小优先,同代价宽度小优先),banned 永不加入。 */
function fill(input: KeepSetInput, kept: boolean[], banned: number): void {
  for (;;) {
    let bestIdx = -1;
    let bestCost = Infinity;
    for (let i = 0; i < kept.length; i++) {
      if (kept[i] || i === banned) continue;
      kept[i] = true;
      const c = costOf(kept, input.widths, input.ellipsisWidth);
      kept[i] = false;
      if (c <= input.lineWidth &&
          (c < bestCost || (c === bestCost && bestIdx >= 0 && input.widths[i] < input.widths[bestIdx]))) {
        bestIdx = i;
        bestCost = c;
      }
    }
    if (bestIdx < 0) return;
    kept[bestIdx] = true;
  }
}

/**
 * 贪心:先保留全部关键原子,填充后做交换改进(移除并禁回,跳出局部最优),
 * 先单点交换,收敛后再试成对交换。
 */
export function greedyKeepSet(input: KeepSetInput): KeepSetResult {
  const n = input.widths.length;
  const kept = input.keyAtoms.slice();
  const initialCost = costOf(kept, input.widths, input.ellipsisWidth);
  if (initialCost > input.lineWidth) {
    // 关键片段互相超容量:原样返回,由调用方输出冲突
    return { kept, feasible: false, cost: initialCost, score: scoreOf(kept, input.keyAtoms) };
  }
  fill(input, kept, -1);
  for (let improved = true; improved;) {
    improved = false;
    for (let i = 0; i < n && !improved; i++) {
      if (!kept[i] || input.keyAtoms[i]) continue;
      const trial = kept.slice();
      trial[i] = false;
      fill(input, trial, i);
      if (scoreOf(trial, input.keyAtoms) > scoreOf(kept, input.keyAtoms)) {
        for (let k = 0; k < n; k++) kept[k] = trial[k];
        improved = true;
      }
    }
    if (improved) continue;
    // 成对补入:两个原子一起加才可行(单加会多出一个省略空隙)
    let pairAdded = false;
    addPair: for (let i = 0; i < n; i++) {
      if (kept[i]) continue;
      for (let j = i + 1; j < n; j++) {
        if (kept[j]) continue;
        const trial = kept.slice();
        trial[i] = true;
        trial[j] = true;
        if (costOf(trial, input.widths, input.ellipsisWidth) > input.lineWidth) continue;
        fill(input, trial, -1);
        if (scoreOf(trial, input.keyAtoms) > scoreOf(kept, input.keyAtoms)) {
          for (let k = 0; k < n; k++) kept[k] = trial[k];
          pairAdded = true;
          break addPair;
        }
      }
    }
    if (pairAdded) { improved = true; continue; }
    outer: for (let i = 0; i < n; i++) {
      if (!kept[i] || input.keyAtoms[i]) continue;
      for (let j = i + 1; j < n; j++) {
        if (!kept[j] || input.keyAtoms[j]) continue;
        const trial = kept.slice();
        trial[i] = false;
        trial[j] = false;
        fill(input, trial, i);
        if (trial[j]) continue; // j 被填回,等价于单点交换,已试过
        if (scoreOf(trial, input.keyAtoms) > scoreOf(kept, input.keyAtoms)) {
          for (let k = 0; k < n; k++) kept[k] = trial[k];
          improved = true;
          break outer;
        }
      }
    }
  }
  const finalCost = costOf(kept, input.widths, input.ellipsisWidth);
  return { kept, feasible: true, cost: finalCost, score: scoreOf(kept, input.keyAtoms) };
}
