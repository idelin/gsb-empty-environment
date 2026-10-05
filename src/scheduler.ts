/**
 * 广告排期求解器
 *
 * 模型:
 *  - 离散时段: 最多 20 个时段, 每个时段最多 5 个候选广告 (也可选择空放)。
 *  - 滚动频控: 每个用户组在任意连续 W 个时段的窗口内, 曝光次数不得超过 max。
 *    窗口按"时段序号"滑动, 与日历日无关 —— 跨午夜窗口不会在 0 点清零。
 *  - 相邻品牌互斥: 相邻两个时段不得投放同一品牌, 也不得投放配置为互斥的品牌对。
 *  - 目标: 总收益最大。
 *
 * 算法: 按时段递推的记忆化搜索 (DP)。
 * 状态 = (当前时段下标, 最近 maxWindow-1 个时段的选择)。
 * 由于频控窗口是有界的, 更早的历史对后续决策无影响, 可以安全记忆化。
 */

export interface Candidate {
  adId: string;
  brand: string;
  /** 该广告曝光计入哪些用户组的频控 */
  groups: string[];
  revenue: number;
}

export interface Slot {
  id: string;
  /** 日历日序号, 仅用于展示与跨日窗口验证, 不参与频控计算 */
  day: number;
  candidates: Candidate[];
}

export interface FreqCap {
  group: string;
  /** 滚动窗口长度 (单位: 时段数) */
  windowSlots: number;
  /** 窗口内该用户组允许的最大曝光次数 */
  max: number;
}

export interface SolveOptions {
  caps?: FreqCap[];
  /** 额外的互斥品牌对 (同品牌相邻默认互斥, 无需配置) */
  exclusiveBrandPairs?: Array<[string, string]>;
}

export interface WindowCount {
  group: string;
  /** 窗口结束时段下标 (含) */
  endSlot: number;
  /** 窗口起始时段下标 (含), 开头不足一个窗口时为 0 */
  startSlot: number;
  count: number;
  cap: number;
  /** 窗口是否跨越日历日边界 (跨日窗口照常累计, 不在午夜清零) */
  spansMidnight: boolean;
}

export interface SolveResult {
  totalRevenue: number;
  /** 每个时段选中的候选 (null = 空放) */
  choices: Array<Candidate | null>;
  windowCounts: WindowCount[];
}

export const MAX_SLOTS = 20;
export const MAX_CANDIDATES_PER_SLOT = 5;

function validate(slots: Slot[]): void {
  if (slots.length === 0) throw new Error("至少需要一个时段");
  if (slots.length > MAX_SLOTS) {
    throw new Error(`时段数 ${slots.length} 超过上限 ${MAX_SLOTS}`);
  }
  for (const slot of slots) {
    if (slot.candidates.length > MAX_CANDIDATES_PER_SLOT) {
      throw new Error(
        `时段 ${slot.id} 候选数 ${slot.candidates.length} 超过上限 ${MAX_CANDIDATES_PER_SLOT}`,
      );
    }
  }
}

export function solve(slots: Slot[], options: SolveOptions = {}): SolveResult {
  validate(slots);
  const caps = options.caps ?? [];
  const n = slots.length;

  // 互斥品牌对 -> 邻接表
  const exclusive = new Map<string, Set<string>>();
  const addPair = (a: string, b: string) => {
    if (!exclusive.has(a)) exclusive.set(a, new Set());
    exclusive.get(a)!.add(b);
  };
  for (const [a, b] of options.exclusiveBrandPairs ?? []) {
    addPair(a, b);
    addPair(b, a);
  }
  const conflictsWith = (prevBrand: string, brand: string): boolean =>
    prevBrand === brand || (exclusive.get(prevBrand)?.has(brand) ?? false);

  // 每个候选广告 -> 其用户组对应的频控下标列表
  const capIndexByGroup = new Map<string, number[]>();
  caps.forEach((cap, i) => {
    const list = capIndexByGroup.get(cap.group) ?? [];
    list.push(i);
    capIndexByGroup.set(cap.group, list);
  });

  const maxWindow = caps.reduce((m, c) => Math.max(m, c.windowSlots), 0);
  // 历史中每个元素的编码: 0 = 空放, k+1 = 该时段第 k 个候选
  // 至少保留最近 1 个时段: 相邻品牌互斥依赖上一时段的选择
  const tailLen = Math.max(1, maxWindow - 1);

  // history[j] 为时段 j 的选择编码
  const history: number[] = new Array<number>(n).fill(0);
  const memo = new Map<string, number>();

  /** 在时段 i 投放候选 cand 是否满足所有滚动频控 (基于 history[0..i-1]) */
  const fitsCaps = (i: number, cand: Candidate): boolean => {
    for (const group of cand.groups) {
      for (const ci of capIndexByGroup.get(group) ?? []) {
        const cap = caps[ci]!;
        const start = Math.max(0, i - cap.windowSlots + 1);
        let count = 1; // 当前这次投放
        for (let j = start; j < i; j++) {
          const code = history[j]!;
          if (code > 0 && slots[j]!.candidates[code - 1]!.groups.includes(group)) {
            count++;
          }
        }
        if (count > cap.max) return false;
      }
    }
    return true;
  };

  /** 从时段 i 开始的最大收益 (history[0..i-1] 已确定) */
  const dfs = (i: number): number => {
    if (i === n) return 0;
    let key = `${i}#`;
    for (let j = Math.max(0, i - tailLen); j < i; j++) key += history[j]!.toString(36);
    const hit = memo.get(key);
    if (hit !== undefined) return hit;

    // 选项 0: 空放
    history[i] = 0;
    let best = dfs(i + 1);

    const prevCode = i > 0 ? history[i - 1]! : 0;
    const prevBrand =
      prevCode > 0 ? slots[i - 1]!.candidates[prevCode - 1]!.brand : null;

    const cands = slots[i]!.candidates;
    for (let k = 0; k < cands.length; k++) {
      const cand = cands[k]!;
      if (prevBrand !== null && conflictsWith(prevBrand, cand.brand)) continue;
      if (!fitsCaps(i, cand)) continue;
      history[i] = k + 1;
      const value = cand.revenue + dfs(i + 1);
      if (value > best) best = value;
    }
    history[i] = 0;
    memo.set(key, best);
    return best;
  };

  const totalRevenue = dfs(0);

  // 回放最优选择
  const choices: Array<Candidate | null> = [];
  const replay = (i: number): void => {
    if (i === n) return;
    let key = `${i}#`;
    for (let j = Math.max(0, i - tailLen); j < i; j++) key += history[j]!.toString(36);
    const target = memo.get(key)!;

    history[i] = 0;
    if (dfs(i + 1) === target) {
      choices.push(null);
      replay(i + 1);
      return;
    }
    const prevCode = i > 0 ? history[i - 1]! : 0;
    const prevBrand =
      prevCode > 0 ? slots[i - 1]!.candidates[prevCode - 1]!.brand : null;
    const cands = slots[i]!.candidates;
    for (let k = 0; k < cands.length; k++) {
      const cand = cands[k]!;
      if (prevBrand !== null && conflictsWith(prevBrand, cand.brand)) continue;
      if (!fitsCaps(i, cand)) continue;
      history[i] = k + 1;
      if (cand.revenue + dfs(i + 1) === target) {
        choices.push(cand);
        replay(i + 1);
        return;
      }
    }
    history[i] = 0;
    throw new Error("回放失败: 未找到与最优值一致的选择");
  };
  replay(0);

  return { totalRevenue, choices, windowCounts: countWindows(slots, choices, caps) };
}

/**
 * 统计每个用户组在每个滚动窗口内的曝光计数。
 * 窗口完全按时段序号滑动, 不读取 day 字段 —— 因此跨午夜窗口不会清零;
 * spansMidnight 仅用于标注哪些窗口跨越了日历日边界。
 */
export function countWindows(
  slots: Slot[],
  choices: Array<Candidate | null>,
  caps: FreqCap[],
): WindowCount[] {
  const result: WindowCount[] = [];
  for (const cap of caps) {
    for (let end = 0; end < slots.length; end++) {
      const start = Math.max(0, end - cap.windowSlots + 1);
      let count = 0;
      let spansMidnight = false;
      for (let j = start; j <= end; j++) {
        const ad = choices[j];
        if (ad && ad.groups.includes(cap.group)) count++;
        if (slots[j]!.day !== slots[end]!.day) spansMidnight = true;
      }
      result.push({ group: cap.group, endSlot: end, startSlot: start, count, cap: cap.max, spansMidnight });
    }
  }
  return result;
}

/**
 * 校验一份计划是否满足全部约束 (用于测试与演示自检)。
 * 返回违规描述列表, 空数组表示合法。
 */
export function verifyPlan(
  slots: Slot[],
  choices: Array<Candidate | null>,
  options: SolveOptions = {},
): string[] {
  const violations: string[] = [];
  const caps = options.caps ?? [];

  // 相邻品牌互斥
  const exclusive = new Map<string, Set<string>>();
  for (const [a, b] of options.exclusiveBrandPairs ?? []) {
    if (!exclusive.has(a)) exclusive.set(a, new Set());
    exclusive.get(a)!.add(b);
    if (!exclusive.has(b)) exclusive.set(b, new Set());
    exclusive.get(b)!.add(a);
  }
  for (let i = 1; i < choices.length; i++) {
    const prev = choices[i - 1];
    const cur = choices[i];
    if (!prev || !cur) continue;
    if (prev.brand === cur.brand || (exclusive.get(prev.brand)?.has(cur.brand) ?? false)) {
      violations.push(`时段 ${i - 1} 与 ${i} 投放了互斥品牌 ${prev.brand}/${cur.brand}`);
    }
  }

  // 滚动频控 (按时段滑窗, 跨日不清零)
  for (const w of countWindows(slots, choices, caps)) {
    if (w.count > w.cap) {
      violations.push(
        `用户组 ${w.group} 在窗口 [${w.startSlot}, ${w.endSlot}] 曝光 ${w.count} 次, 超过上限 ${w.cap}`,
      );
    }
  }
  return violations;
}
