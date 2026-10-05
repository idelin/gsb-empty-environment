/**
 * 单机排程精确求解器
 * 问题: 1 | r_j, 序列相关换线 s_ij(有向), 交货期 d_j, 单位迟交罚金 w_j | min Σ w_j·T_j
 * 次目标: 总罚金相同时, 最小化最大完工时间 (makespan)
 * 规模: n <= 18
 *
 * 方法: 子集 DP (Held-Karp 风格)。状态 (mask, last) 保存一组
 * Pareto 最优的 (完工时间 t, 累计罚金 p) 二元组:
 *   - 同一 (mask, last) 下, "罚金低但完工晚" 与 "罚金高但完工早" 互不支配,
 *     只按字典序保留单值会丢解, 因此保留整个 Pareto 前沿。
 *   - 后续罚金是关于当前完工时间的单调不减函数, 故被支配状态可安全剪枝, 算法精确。
 *
 * 空闲: 开工时间 = max(机器空闲 + 换线, 释放时间)。等待释放产生的空闲被自然建模;
 * 对正则目标(迟交罚金)额外主动空闲不会更优, 且次目标要求最早完工, 因此不会引入多余空闲。
 */

export interface Job {
  id: string;
  release: number; // 释放时间 r_j
  proc: number;    // 加工时长 p_j
  due: number;     // 交货期 d_j
  weight: number;  // 单位时间迟交罚金 w_j
}

export interface Problem {
  jobs: Job[];
  /** setup[i][j]: 紧接在任务 i 之后加工任务 j 所需的换线时间 (i === j 的值忽略) */
  setup: number[][];
  /** 机器初始状态到各任务的换线时间, 缺省全 0 */
  initialSetup?: number[];
  /**
   * 换线是否可先于释放时间开始 (anticipatory setup), 缺省 true:
   * true  -> 机器一空闲即可换线, 之后若任务未释放则等待;
   * false -> 任务释放后才开始换线。
   */
  anticipatorySetup?: boolean;
}

export interface Segment {
  jobIndex: number;
  jobId: string;
  prevEnd: number;        // 上一任务完工时刻 (首段为 0)
  waitBeforeSetup: number; // 换线前的空闲 (仅非抢占式换线时出现)
  setupStart: number;
  setupLen: number;
  waitAfterSetup: number;  // 换线完成后等待释放的空闲
  start: number;          // 加工开始
  end: number;            // 加工结束 (完工时间)
  tardiness: number;
  penalty: number;
}

export interface Solution {
  order: number[];      // 任务下标顺序
  segments: Segment[];
  totalPenalty: number; // 主目标: 总迟交罚金
  makespan: number;     // 次目标: 最大完工时间
  totalIdle: number;
  statesExplored: number;
}

/** Pareto 前沿元素: t 完工时间, p 累计罚金, (pm, pl, pi) 父状态指针 */
interface Entry {
  t: number;
  p: number;
  pm: number; // 父 mask
  pl: number; // 父 last
  pi: number; // 父前沿中的下标
}

export function solve(problem: Problem): Solution {
  const jobs = problem.jobs;
  const n = jobs.length;
  if (n === 0) {
    return { order: [], segments: [], totalPenalty: 0, makespan: 0, totalIdle: 0, statesExplored: 0 };
  }
  if (n > 18) {
    throw new Error(`任务数 ${n} 超过上限 18`);
  }
  const setup = problem.setup;
  const initialSetup = problem.initialSetup ?? new Array<number>(n).fill(0);
  const anticip = problem.anticipatorySetup ?? true;

  /** 从机器空闲时刻 free 加工任务 k 后的完工时间 */
  const finishTime = (free: number, prev: number, k: number): number => {
    const s = prev < 0 ? initialSetup[k] : setup[prev][k];
    if (anticip) {
      return Math.max(free + s, jobs[k].release) + jobs[k].proc;
    }
    return Math.max(free, jobs[k].release) + s + jobs[k].proc;
  };
  const tardinessPenalty = (k: number, end: number): number =>
    jobs[k].weight * Math.max(0, end - jobs[k].due);

  const size = 1 << n;
  // fronts[mask][last] = 按 t 升序、p 严格降序排列的 Pareto 前沿
  const fronts: ((Entry[] | null)[] | null)[] = new Array(size).fill(null);
  let statesExplored = 0;

  const rowOf = (mask: number): (Entry[] | null)[] => {
    let row = fronts[mask];
    if (row === null) {
      row = new Array<Entry[] | null>(n).fill(null);
      fronts[mask] = row;
    }
    return row;
  };

  /** 向前沿插入元素, 维护 Pareto 最优性; 返回插入后的前沿 */
  const insert = (front: Entry[] | null, e: Entry): Entry[] => {
    if (front === null) return [e];
    // 找第一个 t >= e.t 的位置
    let lo = 0;
    let hi = front.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (front[mid].t < e.t) lo = mid + 1; else hi = mid;
    }
    // 支配检查: lo-1 是 t <= e.t 中 p 最小者 (p 随 t 递减)
    if (lo > 0 && front[lo - 1].p <= e.p) return front;
    // 删除被 e 支配的元素: t >= e.t 且 p >= e.p (它们是 t>=e.t 区间的前缀)
    let end = lo;
    while (end < front.length && front[end].p >= e.p) end++;
    front.splice(lo, end - lo, e);
    return front;
  };

  // 初始状态: 从机器初始状态 (prev = -1) 开始
  for (let k = 0; k < n; k++) {
    const t = finishTime(0, -1, k);
    const e: Entry = { t, p: tardinessPenalty(k, t), pm: -1, pl: -1, pi: -1 };
    const row = rowOf(1 << k);
    row[k] = insert(row[k], e);
  }

  // 子集 DP: mask 递增枚举, 其超集数值必更大, 顺序安全
  for (let mask = 1; mask < size; mask++) {
    const row = fronts[mask];
    if (row === null) continue;
    for (let last = 0; last < n; last++) {
      const front = row[last];
      if (front === null) continue;
      statesExplored += front.length;
      for (let idx = 0; idx < front.length; idx++) {
        const e = front[idx];
        for (let k = 0; k < n; k++) {
          const bit = 1 << k;
          if (mask & bit) continue;
          const t = finishTime(e.t, last, k);
          const next: Entry = {
            t,
            p: e.p + tardinessPenalty(k, t),
            pm: mask,
            pl: last,
            pi: idx,
          };
          const nrow = rowOf(mask | bit);
          nrow[k] = insert(nrow[k], next);
        }
      }
    }
  }

  // 终态选择: 主目标罚金最小, 次目标完工最早
  const full = size - 1;
  const fullRow = fronts[full]!;
  let best: Entry | null = null;
  let bestLast = -1;
  for (let last = 0; last < n; last++) {
    const front = fullRow[last];
    if (front === null) continue;
    for (const e of front) {
      if (best === null || e.p < best.p || (e.p === best.p && e.t < best.t)) {
        best = e;
        bestLast = last;
      }
    }
  }
  if (best === null) throw new Error("无可行解");

  // 回溯任务顺序
  const order: number[] = [];
  let curMask = full;
  let curLast = bestLast;
  let cur: Entry = best;
  while (curLast >= 0) {
    order.push(curLast);
    if (cur.pm < 0) break;
    curMask = cur.pm;
    curLast = cur.pl;
    cur = fronts[curMask]![curLast]![cur.pi];
  }
  order.reverse();

  // 按时间规则重演, 生成每段的等待/换线明细
  const segments: Segment[] = [];
  let free = 0;
  let totalIdle = 0;
  for (const k of order) {
    const s = segments.length === 0 ? initialSetup[k] : setup[segments[segments.length - 1].jobIndex][k];
    let waitBeforeSetup = 0;
    let setupStart: number;
    if (anticip) {
      setupStart = free;
    } else {
      waitBeforeSetup = Math.max(0, jobs[k].release - free);
      setupStart = free + waitBeforeSetup;
    }
    const setupEnd = setupStart + s;
    const waitAfterSetup = Math.max(0, jobs[k].release - setupEnd);
    const start = setupEnd + waitAfterSetup;
    const end = start + jobs[k].proc;
    const tardiness = Math.max(0, end - jobs[k].due);
    totalIdle += waitBeforeSetup + waitAfterSetup;
    segments.push({
      jobIndex: k,
      jobId: jobs[k].id,
      prevEnd: free,
      waitBeforeSetup,
      setupStart,
      setupLen: s,
      waitAfterSetup,
      start,
      end,
      due: jobs[k].due,
      tardiness,
      penalty: jobs[k].weight * tardiness,
    });
    free = end;
  }

  return {
    order,
    segments,
    totalPenalty: best.p,
    makespan: best.t,
    totalIdle,
    statesExplored,
  };
}
