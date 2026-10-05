/**
 * 权限规则排序器核心求解逻辑。
 *
 * 模型：
 * - 规则按顺序评估，首次匹配生效（first-match）。
 * - 只允许交换相邻规则，目标是用最少交换次数重排规则，使全部样例被正确分类。
 * - 最少相邻交换次数 = 新顺序相对原顺序的逆序对数。
 *
 * 算法（n <= 15）：
 * - 子集 DP：dp[mask] = 将 mask 中的规则放在前 popcount(mask) 个位置、
 *   且不违反任何"已确定"样例约束的最小逆序对数。
 * - 样例约束：设样例 s 匹配的规则集为 M，其中动作正确的规则集为 C。
 *   约束等价于"M 中在最终顺序里排最前的规则必须属于 C"。
 *   DP 按前缀构建顺序，当放置规则 r 且 r 是 M 中第一个被放置的
 *   （即 mask & M == 0）时，要求 r ∈ C，否则剪枝。
 * - 无解时通过删除式极小化找出一个包含极小的冲突样例子集（不修改、不丢弃样例）。
 */

export type Action = 'allow' | 'deny';

export interface Rule {
  id: string;
  action: Action;
  /** 条件：每个键要求样例属性等值匹配；值为 "*" 表示通配。 */
  when: Record<string, string>;
}

export interface Sample {
  id: string;
  expect: Action;
  attrs: Record<string, string>;
}

export interface Problem {
  rules: Rule[];
  samples: Sample[];
}

export interface RuleRef {
  id: string;
  action: Action;
}

export interface SampleHit {
  sampleId: string;
  expect: Action;
  ruleId: string;
  action: Action;
}

export interface SolveOk {
  ok: true;
  /** 新顺序下的规则 id 列表 */
  order: string[];
  /** 最少相邻交换次数 */
  swaps: number;
  /** 每个样例命中的规则 */
  hits: SampleHit[];
}

export interface ConflictSample {
  id: string;
  expect: Action;
  matchingRules: RuleRef[];
}

export interface SolveConflict {
  ok: false;
  /** 包含极小的一组互相冲突、无法同时满足的样例 */
  conflictSamples: ConflictSample[];
}

export type SolveResult = SolveOk | SolveConflict;

export const MAX_RULES = 15;

export function ruleMatches(rule: Rule, sample: Sample): boolean {
  for (const key of Object.keys(rule.when)) {
    const want = rule.when[key];
    if (want === '*') continue;
    if (sample.attrs[key] !== want) return false;
  }
  return true;
}

export function validate(problem: Problem): void {
  if (!Array.isArray(problem.rules)) throw new Error('rules 必须是数组');
  if (!Array.isArray(problem.samples)) throw new Error('samples 必须是数组');
  if (problem.rules.length > MAX_RULES) {
    throw new Error(`规则数 ${problem.rules.length} 超过上限 ${MAX_RULES}`);
  }
  const ruleIds = new Set<string>();
  for (const rule of problem.rules) {
    if (typeof rule.id !== 'string' || rule.id === '') throw new Error('规则缺少 id');
    if (ruleIds.has(rule.id)) throw new Error(`规则 id 重复: ${rule.id}`);
    ruleIds.add(rule.id);
    if (rule.action !== 'allow' && rule.action !== 'deny') {
      throw new Error(`规则 ${rule.id} 的 action 非法: ${String(rule.action)}`);
    }
    if (typeof rule.when !== 'object' || rule.when === null) {
      throw new Error(`规则 ${rule.id} 缺少 when 条件对象`);
    }
  }
  const sampleIds = new Set<string>();
  for (const sample of problem.samples) {
    if (typeof sample.id !== 'string' || sample.id === '') throw new Error('样例缺少 id');
    if (sampleIds.has(sample.id)) throw new Error(`样例 id 重复: ${sample.id}`);
    sampleIds.add(sample.id);
    if (sample.expect !== 'allow' && sample.expect !== 'deny') {
      throw new Error(`样例 ${sample.id} 的 expect 非法: ${String(sample.expect)}`);
    }
    if (typeof sample.attrs !== 'object' || sample.attrs === null) {
      throw new Error(`样例 ${sample.id} 缺少 attrs 属性对象`);
    }
  }
}

interface SampleMasks {
  index: number;
  /** 匹配该样例的规则位掩码 */
  matchMask: number;
  /** 匹配且动作符合期望的规则位掩码 */
  correctMask: number;
}

function popcount(x: number): number {
  x = x - ((x >> 1) & 0x55555555);
  x = (x & 0x33333333) + ((x >> 2) & 0x33333333);
  return (((x + (x >> 4)) & 0x0f0f0f0f) * 0x01010101) >> 24;
}

interface DpResult {
  /** 最小交换次数；Infinity 表示无解 */
  swaps: number;
  /** parent[mask] = 最优转移来源的前驱 mask，用于回溯 */
  parent: Int32Array;
}

/**
 * 子集 DP。sms 为参与约束的样例掩码列表。
 * 原顺序即规则数组下标顺序，规则 r 之前未放置且原下标小于 r 的规则数
 * 即为放置 r 产生的逆序对增量。
 */
function dpSolve(n: number, sms: SampleMasks[]): DpResult {
  const size = 1 << n;
  const dp = new Float64Array(size).fill(Infinity);
  const parent = new Int32Array(size).fill(-1);
  dp[0] = 0;

  // wrongFirst[r]：若规则 r 是这些样例匹配集中第一个被放置的，则违反约束
  const wrongFirst: number[][] = Array.from({ length: n }, () => []);
  for (const sm of sms) {
    const wrongMask = sm.matchMask & ~sm.correctMask;
    let bits = wrongMask;
    while (bits !== 0) {
      const bit = bits & -bits;
      const r = 31 - Math.clz32(bit);
      wrongFirst[r].push(sm.matchMask);
      bits ^= bit;
    }
  }

  for (let mask = 0; mask < size; mask++) {
    const cur = dp[mask];
    if (cur === Infinity) continue;
    for (let r = 0; r < n; r++) {
      const bit = 1 << r;
      if ((mask & bit) !== 0) continue;
      const constraints = wrongFirst[r];
      let blocked = false;
      for (let k = 0; k < constraints.length; k++) {
        // r 是某样例匹配集中第一个被放置的错误动作规则 -> 剪枝
        if ((mask & constraints[k]) === 0) {
          blocked = true;
          break;
        }
      }
      if (blocked) continue;
      const cost = popcount(((1 << r) - 1) & ~mask);
      const next = mask | bit;
      const value = cur + cost;
      if (value < dp[next]) {
        dp[next] = value;
        parent[next] = mask;
      }
    }
  }
  return { swaps: dp[size - 1], parent };
}

function buildMasks(problem: Problem): SampleMasks[] {
  return problem.samples.map((sample, index) => {
    let matchMask = 0;
    let correctMask = 0;
    problem.rules.forEach((rule, i) => {
      if (ruleMatches(rule, sample)) {
        matchMask |= 1 << i;
        if (rule.action === sample.expect) correctMask |= 1 << i;
      }
    });
    return { index, matchMask, correctMask };
  });
}

/** 判断给定样例下标子集是否可同时满足（不修改任何样例）。 */
export function feasibleSubset(problem: Problem, sampleIndexes: number[]): boolean {
  const masks = buildMasks(problem);
  const picked = sampleIndexes.map((i) => masks[i]);
  if (picked.some((sm) => sm.matchMask === 0)) return false;
  return dpSolve(problem.rules.length, picked).swaps !== Infinity;
}

/** 删除式极小化：返回一个包含极小的不可满足样例下标子集。 */
function minimizeConflict(
  all: number[],
  feasible: (idxs: number[]) => boolean,
): number[] {
  let current = [...all];
  let i = 0;
  while (i < current.length) {
    const trial = [...current.slice(0, i), ...current.slice(i + 1)];
    if (!feasible(trial)) {
      current = trial;
    } else {
      i++;
    }
  }
  return current;
}

export function solve(problem: Problem): SolveResult {
  validate(problem);
  const n = problem.rules.length;
  const masks = buildMasks(problem);
  const allIdx = problem.samples.map((_, i) => i);
  const feasible = (idxs: number[]) => {
    const picked = idxs.map((i) => masks[i]);
    if (picked.some((sm) => sm.matchMask === 0)) return false;
    return dpSolve(n, picked).swaps !== Infinity;
  };

  if (!feasible(allIdx)) {
    const conflictIdxs = minimizeConflict(allIdx, feasible);
    return {
      ok: false,
      conflictSamples: conflictIdxs.map((i) => ({
        id: problem.samples[i].id,
        expect: problem.samples[i].expect,
        matchingRules: problem.rules
          .filter((_, j) => ((masks[i].matchMask >> j) & 1) === 1)
          .map((r) => ({ id: r.id, action: r.action })),
      })),
    };
  }

  const { swaps, parent } = dpSolve(n, masks);
  const orderIdx: number[] = [];
  let mask = (1 << n) - 1;
  while (mask !== 0) {
    const prev = parent[mask];
    const r = 31 - Math.clz32(mask ^ prev);
    orderIdx.push(r);
    mask = prev;
  }
  orderIdx.reverse();

  const orderedRules = orderIdx.map((i) => problem.rules[i]);
  const hits: SampleHit[] = problem.samples.map((sample) => {
    for (const rule of orderedRules) {
      if (ruleMatches(rule, sample)) {
        return { sampleId: sample.id, expect: sample.expect, ruleId: rule.id, action: rule.action };
      }
    }
    // 不可达：可行性已保证每个样例至少匹配一条规则
    throw new Error(`样例 ${sample.id} 无匹配规则`);
  });

  return {
    ok: true,
    order: orderedRules.map((r) => r.id),
    swaps,
    hits,
  };
}
