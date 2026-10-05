import { Problem } from './types';

/** 搜索节点预算耗尽时抛出。 */
export class BudgetExceeded extends Error {
  constructor() {
    super('search node budget exceeded');
    this.name = 'BudgetExceeded';
  }
}

/** 归一化后的内部表示：全部使用下标，避免字符串比较。 */
export interface Normalized {
  K: number;
  P: number;
  maxWorks: number;
  nodeLimit: number;
  workIds: string[];
  judgeIds: string[];
  workFields: string[];
  judgeFields: string[][];
  /** conflicted[j][w]：评委 j 是否回避作品 w。 */
  conflicted: boolean[][];
  /** candidates[w]：可评审作品 w 的评委下标（专业匹配且未回避）。 */
  candidates: number[][];
}

export function normalize(problem: Problem): Normalized {
  const K = problem.judgesPerWork ?? 3;
  const P = problem.maxPairCoJudging ?? 2;
  const maxWorks = problem.maxWorks ?? 12;
  const nodeLimit = problem.nodeLimit ?? 1_000_000;
  if (!Number.isInteger(K) || K < 1) throw new Error('judgesPerWork 必须为正整数');
  if (!Number.isInteger(P) || P < 1) throw new Error('maxPairCoJudging 必须为正整数');
  if (!Number.isInteger(maxWorks) || maxWorks < 1) throw new Error('maxWorks 必须为正整数');

  const workIds = problem.works.map((w) => w.id);
  const judgeIds = problem.judges.map((j) => j.id);
  if (new Set(workIds).size !== workIds.length) throw new Error('作品 id 存在重复');
  if (new Set(judgeIds).size !== judgeIds.length) throw new Error('评委 id 存在重复');

  const wIndex = new Map(workIds.map((id, i) => [id, i] as const));
  const jIndex = new Map(judgeIds.map((id, i) => [id, i] as const));

  const W = workIds.length;
  const J = judgeIds.length;
  const conflicted: boolean[][] = Array.from({ length: J }, () => new Array<boolean>(W).fill(false));
  for (const c of problem.conflicts ?? []) {
    const ji = jIndex.get(c.judgeId);
    const wi = wIndex.get(c.workId);
    if (ji === undefined || wi === undefined) {
      throw new Error(`回避关系引用了未知 id：judgeId=${c.judgeId}, workId=${c.workId}`);
    }
    conflicted[ji][wi] = true;
  }

  const candidates: number[][] = problem.works.map((w, wi) => {
    const out: number[] = [];
    problem.judges.forEach((j, ji) => {
      if (j.fields.includes(w.field) && !conflicted[ji][wi]) out.push(ji);
    });
    return out;
  });

  return {
    K,
    P,
    maxWorks,
    nodeLimit,
    workIds,
    judgeIds,
    workFields: problem.works.map((w) => w.field),
    judgeFields: problem.judges.map((j) => j.fields),
    conflicted,
    candidates,
  };
}

/** 从 items 中取 k 个的全部组合（按下标升序）。 */
export function combinations(items: number[], k: number): number[][] {
  const out: number[][] = [];
  const cur: number[] = [];
  const rec = (start: number): void => {
    if (cur.length === k) {
      out.push(cur.slice());
      return;
    }
    const need = k - cur.length;
    for (let i = start; i <= items.length - need; i++) {
      cur.push(items[i]);
      rec(i + 1);
      cur.pop();
    }
  };
  if (k >= 0 && items.length >= k) rec(0);
  return out;
}

export interface SearchEnv {
  J: number;
  K: number;
  P: number;
  nodeLimit: number;
  nodes: { count: number };
}

/**
 * 回溯搜索：为每份作品选择一个评委组合，使所有评委工作量落在 [lo, hi]，
 * 且任意评委对共评次数不超过 P。成功返回每份作品的评委下标组合，失败返回 null。
 * 完备性：在节点预算内是精确判定（不是启发式）。
 */
export function search(combosPerWork: number[][][], env: SearchEnv, lo: number, hi: number): number[][] | null {
  const { J, K, P } = env;
  const W = combosPerWork.length;
  const load = new Array<number>(J).fill(0);
  const pair = new Uint16Array(J * J);
  const chosen: number[][] = new Array<number[]>(W);
  const assigned = new Array<boolean>(W).fill(false);
  // 静态顺序：候选组合少的作品优先，配合动态 MRV。
  const order = Array.from(combosPerWork.keys()).sort((a, b) => combosPerWork[a].length - combosPerWork[b].length);

  const comboOk = (combo: number[]): boolean => {
    for (let i = 0; i < combo.length; i++) {
      const a = combo[i];
      if (load[a] + 1 > hi) return false;
      for (let j = i + 1; j < combo.length; j++) {
        if (pair[a * J + combo[j]] >= P) return false;
      }
    }
    return true;
  };

  // 统计作品 w 当前可行组合数，达到 limit 即提前退出。
  const countFeasible = (w: number, limit: number): number => {
    let c = 0;
    const combos = combosPerWork[w];
    for (let i = 0; i < combos.length; i++) {
      if (comboOk(combos[i])) {
        c++;
        if (c >= limit) return c;
      }
    }
    return c;
  };

  // 必要条件剪枝（只用于剪枝，不影响完备性）。
  const pruned = (rem: number): boolean => {
    let deficit = 0;
    let capacity = 0;
    for (let j = 0; j < J; j++) {
      let residual = 0;
      const base = j * J;
      for (let m = 0; m < J; m++) {
        if (m !== j) residual += Math.max(0, P - pair[base + m]);
      }
      // 评委 j 每多评一份作品要消耗 K-1 个“评委对余量”。
      const maxAdd = K === 1 ? rem : Math.min(rem, Math.floor(residual / (K - 1)));
      const d = lo - load[j];
      if (d > maxAdd) return true; // 永远到不了下界
      if (d > 0) deficit += d;
      capacity += Math.min(hi - load[j], maxAdd);
    }
    if (deficit > K * rem) return true; // 剩余席位填不满工作量下界
    if (capacity < K * rem) return true; // 剩余席位放不下所有作品
    return false;
  };

  const comboKey = (combo: number[]): number => {
    let loadSum = 0;
    let pairSum = 0;
    for (let i = 0; i < combo.length; i++) {
      loadSum += load[combo[i]];
      for (let j = i + 1; j < combo.length; j++) pairSum += pair[combo[i] * J + combo[j]];
    }
    return loadSum * 64 + pairSum;
  };

  const apply = (combo: number[], delta: number): void => {
    for (let i = 0; i < combo.length; i++) {
      load[combo[i]] += delta;
      for (let j = i + 1; j < combo.length; j++) pair[combo[i] * J + combo[j]] += delta;
    }
  };

  const rec = (rem: number): boolean => {
    if (++env.nodes.count > env.nodeLimit) throw new BudgetExceeded();
    if (pruned(rem)) return false;
    if (rem === 0) return true;

    // MRV：选当前可行组合最少的未分配作品。
    let bestW = -1;
    let bestCount = Infinity;
    for (const w of order) {
      if (assigned[w]) continue;
      const c = countFeasible(w, bestCount);
      if (c === 0) return false;
      if (c < bestCount) {
        bestCount = c;
        bestW = w;
        if (c === 1) break;
      }
    }
    const combos = combosPerWork[bestW].filter(comboOk);
    // 优先使用当前工作量低、评委对使用少的组合，倾向于均衡解。
    combos.sort((x, y) => comboKey(x) - comboKey(y));
    for (const combo of combos) {
      apply(combo, 1);
      assigned[bestW] = true;
      chosen[bestW] = combo;
      if (rec(rem - 1)) return true;
      assigned[bestW] = false;
      apply(combo, -1);
    }
    return false;
  };

  return rec(W) ? chosen : null;
}
