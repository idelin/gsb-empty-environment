/**
 * 权限规则排序器（相邻交换 / 首次匹配）
 *
 * 问题：给定 N 条规则（N <= 15，按首匹配生效）和若干 allow/deny 样例，
 * 只允许交换相邻规则，求满足全部样例的最少交换次数。
 * 无解时给出极小冲突样例子集；绝不修改样例。
 */

export type Action = 'allow' | 'deny';

export interface Rule {
  id: string;
  action: Action;
  /** 通配符模式：`*` 匹配任意序列，`?` 匹配单个字符 */
  pattern: string;
}

export interface Sample {
  id: string;
  input: string;
  expect: Action;
}

export interface SampleHit {
  sampleId: string;
  input: string;
  expect: Action;
  hitRuleId: string;
}

export interface SolveOk {
  ok: true;
  /** 新顺序（规则 id，按优先级从高到低） */
  order: string[];
  /** 最少相邻交换次数（= 相对原顺序的逆序对数） */
  swaps: number;
  hits: SampleHit[];
}

export interface SolveConflict {
  ok: false;
  /** 极小冲突样例子集：去掉其中任意一条，其余即可满足 */
  conflictSamples: Sample[];
  reason: string;
}

export type SolveResult = SolveOk | SolveConflict;

function patternToRegExp(pattern: string): RegExp {
  const escaped = pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&')
    .replace(/\*/g, '.*')
    .replace(/\?/g, '.');
  return new RegExp(`^${escaped}$`);
}

export function matchPattern(pattern: string, input: string): boolean {
  return patternToRegExp(pattern).test(input);
}

interface SampleConstraint {
  sample: Sample;
  /** 匹配该样例且动作符合期望的规则下标 */
  good: number[];
  /** 匹配该样例但动作不符合期望的规则下标 */
  bad: number[];
  /** 匹配该样例的全部规则位掩码 */
  mask: number;
}

function buildConstraints(rules: Rule[], samples: Sample[]): SampleConstraint[] {
  return samples.map((sample) => {
    const good: number[] = [];
    const bad: number[] = [];
    let mask = 0;
    rules.forEach((rule, i) => {
      if (matchPattern(rule.pattern, sample.input)) {
        mask |= 1 << i;
        (rule.action === sample.expect ? good : bad).push(i);
      }
    });
    return { sample, good, bad, mask };
  });
}

/**
 * 子集 DP：求满足全部约束、且相对原顺序逆序对数最小的排列。
 * 状态 mask = 已放置规则集合（作为新顺序的前缀）。
 * 转移：放置规则 r 合法，当且仅当对每个“r 是其匹配集中第一个被放置”的样例，
 * r 的动作符合期望（首匹配命中规则必须正确）。
 * 代价：放置 r 时，尚未放置且原下标小于 r 的规则个数（逆序对增量）。
 * 返回 [最小逆序数, 排列]；无解返回 null。
 */
function minInversionOrder(n: number, constraints: SampleConstraint[]): [number, number[]] | null {
  const size = 1 << n;
  const dp = new Float64Array(size).fill(Infinity);
  const parent = new Int32Array(size).fill(-1); // parent[mask] = 最后放置的规则
  dp[0] = 0;

  for (let mask = 0; mask < size; mask++) {
    const base = dp[mask];
    if (base === Infinity) continue;
    for (let r = 0; r < n; r++) {
      const bit = 1 << r;
      if (mask & bit) continue;
      // 合法性：r 不能成为任何样例匹配集中第一个被放置的“错误动作”规则
      let valid = true;
      for (const c of constraints) {
        if ((c.mask & bit) !== 0 && (c.mask & mask) === 0) {
          // r 是该样例匹配集中第一个被放置的规则
          if (!c.good.includes(r)) { valid = false; break; }
        }
      }
      if (!valid) continue;
      // 逆序对增量：未放置且原下标 < r 的规则数
      let cost = 0;
      for (let q = 0; q < r; q++) {
        if ((mask & (1 << q)) === 0) cost++;
      }
      const next = mask | bit;
      if (base + cost < dp[next]) {
        dp[next] = base + cost;
        parent[next] = r;
      }
    }
  }

  const full = size - 1;
  if (dp[full] === Infinity) return null;
  const order: number[] = [];
  let mask = full;
  while (mask !== 0) {
    const r = parent[mask];
    order.push(r);
    mask ^= 1 << r;
  }
  order.reverse();
  return [dp[full], order];
}

/** 检查给定样例集合是否可被满足（供冲突分析复用） */
function isSatisfiable(rules: Rule[], samples: Sample[]): boolean {
  const constraints = buildConstraints(rules, samples);
  for (const c of constraints) {
    if (c.good.length === 0) return false; // 没有任何规则能让它命中正确动作
  }
  return minInversionOrder(rules.length, constraints) !== null;
}

/**
 * 迭代删除法求极小冲突子集（MUS）：
 * 逐个尝试移除样例，若剩余集合仍不可满足则真正移除。
 * 结果保证：子集不可满足，但去掉其中任意一条即可满足。
 */
function findMinimalConflict(rules: Rule[], samples: Sample[]): Sample[] {
  let conflict = [...samples];
  for (const s of [...conflict]) {
    const rest = conflict.filter((x) => x !== s);
    if (!isSatisfiable(rules, rest)) conflict = rest;
  }
  return conflict;
}

export function solve(rules: Rule[], samples: Sample[]): SolveResult {
  if (rules.length === 0) throw new Error('至少需要一条规则');
  if (rules.length > 15) throw new Error(`规则数 ${rules.length} 超过上限 15`);

  const constraints = buildConstraints(rules, samples);

  // 快速失败：某样例没有任何匹配规则，或匹配规则的动作全错
  const hopeless = constraints.find((c) => c.good.length === 0);
  if (hopeless) {
    const reason = hopeless.mask === 0
      ? `样例 "${hopeless.sample.id}" 没有任何规则匹配，调整顺序无法改变`
      : `样例 "${hopeless.sample.id}" 期望 ${hopeless.sample.expect}，但所有匹配规则的动作都相反`;
    return { ok: false, conflictSamples: [hopeless.sample], reason };
  }

  const result = minInversionOrder(rules.length, constraints);
  if (result === null) {
    const conflictSamples = findMinimalConflict(rules, samples);
    return {
      ok: false,
      conflictSamples,
      reason: '不存在满足全部样例的规则顺序（首匹配约束互相冲突）',
    };
  }

  const [swaps, orderIdx] = result;
  const order = orderIdx.map((i) => rules[i].id);
  const hits: SampleHit[] = constraints.map((c) => {
    const first = orderIdx.find((i) => (c.mask & (1 << i)) !== 0)!;
    return {
      sampleId: c.sample.id,
      input: c.sample.input,
      expect: c.sample.expect,
      hitRuleId: rules[first].id,
    };
  });
  return { ok: true, order, swaps, hits };
}
