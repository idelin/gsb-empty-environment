/**
 * 广告排期工具
 *
 * 在离散时段上选择广告，最大化总收益，约束：
 *  1. 用户组滚动频控：任意连续 windowSize 个时段内，同一用户组曝光次数 <= cap。
 *     窗口跨天不重置（午夜不清零），由统一的时间轴保证。
 *  2. 相邻品牌互斥：相邻两个时段不得投放互相冲突的品牌
 *     （默认同品牌互斥，可通过 conflicts 配置竞品互斥对）。
 *
 * 规模：时段数 <= 20，每时段候选广告 <= 5。
 * 解法：按时段做 DP，状态 = (上一时段品牌, 最近 windowSize-1 个时段的用户组序列)。
 */

export interface Ad {
  id: string;
  brand: string;
  group: string;   // 目标用户组
  revenue: number; // 投放收益
}

export interface Slot {
  /** 时段起始时间（跨日时间轴，仅用于展示/校验，不参与状态重置） */
  start: Date;
  candidates: Ad[]; // <= 5
}

export interface ScheduleConfig {
  windowSize: number; // 滚动窗口长度（时段数）
  cap: number;        // 窗口内同一用户组最大曝光次数
  /** 品牌互斥对，双向生效；缺省时同品牌相邻互斥 */
  conflicts?: ReadonlyArray<readonly [string, string]>;
}

export interface Placement {
  slotIndex: number;
  ad: Ad | null; // null 表示该时段空置
}

export interface ScheduleResult {
  placements: Placement[];
  totalRevenue: number;
  /** 每个窗口位置、每个用户组的曝光计数（窗口起点 i 覆盖 [i, i+windowSize)） */
  windowCounts: Array<{ start: number; end: number; counts: Record<string, number> }>;
}

const NONE = -1; // 该时段空置

export function solveSchedule(slots: Slot[], config: ScheduleConfig): ScheduleResult {
  const { windowSize, cap } = config;
  if (slots.length > 20) throw new Error("时段数超过 20");
  for (const s of slots) {
    if (s.candidates.length > 5) throw new Error("单时段候选超过 5 个");
  }

  const conflictSet = new Set<string>();
  if (config.conflicts) {
    for (const [a, b] of config.conflicts) {
      conflictSet.add(a + "" + b);
      conflictSet.add(b + "" + a);
    }
  }
  const brandsConflict = (a: string | null, b: string | null): boolean => {
    if (a === null || b === null) return false;
    if (conflictSet.size > 0) return conflictSet.has(a + "" + b);
    return a === b;
  };

  // 状态键：上一时段品牌 + 最近 windowSize-1 个时段的用户组序列
  interface State {
    prevBrand: string | null;
    hist: string[]; // 长度 <= windowSize-1，最近的在末尾
    revenue: number;
    choices: number[]; // 每个时段选择的候选下标，NONE 表示空置
  }
  const keyOf = (prevBrand: string | null, hist: string[]): string =>
    (prevBrand ?? "") + "|" + hist.join(",");

  let dp = new Map<string, State>();
  dp.set(keyOf(null, []), { prevBrand: null, hist: [], revenue: 0, choices: [] });

  for (let i = 0; i < slots.length; i++) {
    const next = new Map<string, State>();
    const options: Array<Ad | null> = [null, ...slots[i].candidates];
    for (const state of dp.values()) {
      for (const ad of options) {
        if (ad !== null) {
          if (brandsConflict(state.prevBrand, ad.brand)) continue;
          const inWindow = state.hist.filter((g) => g === ad.group).length;
          if (inWindow + 1 > cap) continue; // 滚动频控
        }
        // 空置也占用一个时段：推入占位符，保证窗口按时段滑动而不是按曝光计
        const keep = windowSize - 1;
        const pushedHist =
          keep > 0
            ? [...state.hist, ad === null ? "" : ad.group].slice(-keep)
            : [];
        const prevBrand = ad === null ? null : ad.brand;
        const revenue = state.revenue + (ad === null ? 0 : ad.revenue);
        const key = keyOf(prevBrand, pushedHist);
        const existing = next.get(key);
        if (!existing || existing.revenue < revenue) {
          next.set(key, {
            prevBrand,
            hist: pushedHist,
            revenue,
            choices: [...state.choices, ad === null ? NONE : slots[i].candidates.indexOf(ad)],
          });
        }
      }
    }
    dp = next;
  }

  let best: State | null = null;
  for (const state of dp.values()) {
    if (!best || state.revenue > best.revenue) best = state;
  }
  if (!best) throw new Error("无可行排期");

  const placements: Placement[] = best.choices.map((choice, slotIndex) => ({
    slotIndex,
    ad: choice === NONE ? null : slots[slotIndex].candidates[choice],
  }));

  return {
    placements,
    totalRevenue: best.revenue,
    windowCounts: computeWindowCounts(placements, windowSize),
  };
}

/** 统计每个滚动窗口内各用户组的曝光计数（窗口沿统一时间轴滑动，跨日不清零） */
export function computeWindowCounts(
  placements: Placement[],
  windowSize: number
): ScheduleResult["windowCounts"] {
  const groups = placements.map((p) => (p.ad ? p.ad.group : null));
  const result: ScheduleResult["windowCounts"] = [];
  for (let start = 0; start + windowSize <= placements.length; start++) {
    const counts: Record<string, number> = {};
    for (let i = start; i < start + windowSize; i++) {
      const g = groups[i];
      if (g !== null) counts[g] = (counts[g] ?? 0) + 1;
    }
    result.push({ start, end: start + windowSize - 1, counts });
  }
  return result;
}

/** 校验：所有窗口（含跨午夜的窗口）内各用户组计数均 <= cap */
export function validateSchedule(
  result: ScheduleResult,
  config: ScheduleConfig
): string[] {
  const errors: string[] = [];
  for (const w of result.windowCounts) {
    for (const [group, count] of Object.entries(w.counts)) {
      if (count > config.cap) {
        errors.push(
          `窗口 [${w.start}, ${w.end}] 用户组 ${group} 计数 ${count} 超过上限 ${config.cap}`
        );
      }
    }
  }
  const conflictSet = new Set<string>();
  if (config.conflicts) {
    for (const [a, b] of config.conflicts) {
      conflictSet.add(a + "" + b);
      conflictSet.add(b + "" + a);
    }
  }
  for (let i = 1; i < result.placements.length; i++) {
    const prev = result.placements[i - 1].ad;
    const curr = result.placements[i].ad;
    if (!prev || !curr) continue;
    const conflict =
      conflictSet.size > 0
        ? conflictSet.has(prev.brand + "" + curr.brand)
        : prev.brand === curr.brand;
    if (conflict) {
      errors.push(`时段 ${i - 1} 与 ${i} 品牌互斥冲突: ${prev.brand} / ${curr.brand}`);
    }
  }
  return errors;
}
