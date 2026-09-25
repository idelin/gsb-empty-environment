/**
 * 义务合并求解器。
 *
 * - 每种义务使用各自的合并代数（见 obligations.ts）。
 * - 限额 + 依赖限额构成约束满足问题：在有限候选域上求一组满足全部义务的取值，
 *   并为每个选定的取值生成来源证据（哪些规则贡献了约束、哪个规则决定了边界）。
 * - 不兼容义务（如两个互斥输出格式）导致明确失败，绝不静默择一、绝不静默丢弃。
 * - 不可满足时返回子集最小不可满足核。
 */
import {
  normalizeUnordered,
  type DependentLimitObligation,
  type LimitObligation,
  type Obligation,
} from './obligations.ts';
import { minimalUnsatCore } from './unsat.ts';

export interface LimitBound {
  lo?: number;
  hi?: number;
}

export interface Solution {
  /** 每个限额键最终选定的取值 */
  limits: Record<string, number>;
  /** 每个限额键合并后的上下界 */
  bounds: Record<string, LimitBound>;
  maskedFields: string[];
  auditTags: string[];
  format?: string;
  /** 顺序敏感步骤，显式保序 */
  steps: string[];
}

export interface Evidence {
  limits: Record<string, { value: number; lo?: number; hi?: number; contributors: string[]; decidedBy: string[] }>;
  /** 字段 -> 贡献来源规则 */
  maskedFields: Record<string, string[]>;
  /** 标签 -> 贡献来源规则 */
  auditTags: Record<string, string[]>;
  format?: { value: string; sources: string[] };
  steps: { step: string; source: string; position: number }[];
  dependencies: { key: string; relativeTo: string; minGap: number; source: string }[];
}

export type MergeResult =
  | { status: 'ok'; solution: Solution; evidence: Evidence }
  | { status: 'conflict'; message: string; conflicting: Obligation[] }
  | { status: 'unsat'; message: string; core: Obligation[] };

const ENUM_RANGE_LIMIT = 256;

/** 收集某个限额键的候选取值（有限枚举域）。 */
function candidateValues(
  key: string,
  limits: LimitObligation[],
  deps: DependentLimitObligation[],
  bound: LimitBound,
): number[] {
  const pool = new Set<number>();
  for (const l of limits) if (l.key === key) pool.add(l.value);
  // 依赖传播的候选：relativeTo 的候选值 + minGap
  for (const d of deps) {
    if (d.key !== key) continue;
    for (const l of limits) if (l.key === d.relativeTo) pool.add(l.value + d.minGap);
  }
  if (bound.lo !== undefined && bound.hi !== undefined && bound.hi - bound.lo <= ENUM_RANGE_LIMIT) {
    for (let v = bound.lo; v <= bound.hi; v++) pool.add(v);
  }
  if (bound.lo !== undefined) pool.add(bound.lo);
  if (bound.hi !== undefined) pool.add(bound.hi);
  if (pool.size === 0) pool.add(0);
  return [...pool].filter((v) =>
    (bound.lo === undefined || v >= bound.lo) && (bound.hi === undefined || v <= bound.hi));
}

/** 在有限候选域上回溯求解限额 + 依赖约束。 */
function solveLimits(
  limits: LimitObligation[],
  deps: DependentLimitObligation[],
): Record<string, number> | null {
  const keys = [...new Set([
    ...limits.map((l) => l.key),
    ...deps.flatMap((d) => [d.key, d.relativeTo]),
  ])].sort();

  const bounds: Record<string, LimitBound> = {};
  for (const key of keys) {
    const los = limits.filter((l) => l.key === key && l.direction === 'min').map((l) => l.value);
    const his = limits.filter((l) => l.key === key && l.direction === 'max').map((l) => l.value);
    const bound: LimitBound = {};
    if (los.length > 0) bound.lo = Math.max(...los); // 下限取更严格 = 更大
    if (his.length > 0) bound.hi = Math.min(...his); // 上限取更严格 = 更小
    bounds[key] = bound;
    if (bound.lo !== undefined && bound.hi !== undefined && bound.lo > bound.hi) return null;
  }

  const candidates: Record<string, number[]> = {};
  for (const key of keys) {
    candidates[key] = candidateValues(key, limits, deps, bounds[key]!);
    if (candidates[key]!.length === 0) return null;
  }

  const assignment: Record<string, number> = {};
  const consistent = (key: string, value: number): boolean => {
    for (const d of deps) {
      if (d.key === key && assignment[d.relativeTo] !== undefined) {
        if (value < assignment[d.relativeTo]! + d.minGap) return false;
      }
      if (d.relativeTo === key && assignment[d.key] !== undefined) {
        if (assignment[d.key]! < value + d.minGap) return false;
      }
    }
    return true;
  };

  const search = (i: number): boolean => {
    if (i === keys.length) return true;
    const key = keys[i]!;
    for (const v of candidates[key]!) {
      if (!consistent(key, v)) continue;
      assignment[key] = v;
      if (search(i + 1)) return true;
      delete assignment[key];
    }
    return false;
  };

  return search(0) ? { ...assignment } : null;
}

/** 仅针对限额类约束的可满足性判定（供最小核计算复用）。 */
export function limitsSatisfiable(obligations: Obligation[]): boolean {
  const limits = obligations.filter((o): o is LimitObligation => o.kind === 'limit');
  const deps = obligations.filter((o): o is DependentLimitObligation => o.kind === 'dependentLimit');
  return solveLimits(limits, deps) !== null;
}

export function mergeObligations(obligations: readonly Obligation[]): MergeResult {
  const limits = obligations.filter((o): o is LimitObligation => o.kind === 'limit');
  const masks = obligations.filter((o) => o.kind === 'mask');
  const audits = obligations.filter((o) => o.kind === 'audit');
  const formats = obligations.filter((o) => o.kind === 'format');
  const deps = obligations.filter((o): o is DependentLimitObligation => o.kind === 'dependentLimit');
  const stepObli = obligations.filter((o) => o.kind === 'orderedSteps');

  // --- 输出格式：互斥选择，冲突必须明确失败 ---
  const distinctFormats = [...new Set(formats.map((f) => f.format))];
  if (distinctFormats.length > 1) {
    return {
      status: 'conflict',
      message: `互斥输出格式冲突: ${distinctFormats.join(' vs ')}（不得静默择一）`,
      conflicting: formats,
    };
  }

  // --- 限额 + 依赖限额：求解满足全部义务的方案 ---
  const limitSolution = solveLimits(limits, deps);
  if (limitSolution === null) {
    const core = minimalUnsatCore([...limits, ...deps], limitsSatisfiable);
    return {
      status: 'unsat',
      message: `限额约束不可满足，最小不可满足核含 ${core.length} 条义务`,
      core,
    };
  }

  // --- 遮蔽字段：并集（顺序无关） ---
  const maskedFields = normalizeUnordered(masks.flatMap((m) => m.fields));
  // --- 审计标签：并集（顺序无关，满足交换律） ---
  const auditTags = normalizeUnordered(audits.flatMap((a) => a.tags));
  // --- 顺序敏感步骤：按输入（规则优先级）顺序显式保序拼接 ---
  const steps = stepObli.flatMap((s) => s.steps);

  // --- 组装解 ---
  const bounds: Record<string, LimitBound> = {};
  for (const key of Object.keys(limitSolution)) {
    const los = limits.filter((l) => l.key === key && l.direction === 'min').map((l) => l.value);
    const his = limits.filter((l) => l.key === key && l.direction === 'max').map((l) => l.value);
    const bound: LimitBound = {};
    if (los.length > 0) bound.lo = Math.max(...los);
    if (his.length > 0) bound.hi = Math.min(...his);
    bounds[key] = bound;
  }

  const solution: Solution = {
    limits: limitSolution,
    bounds,
    maskedFields,
    auditTags,
    steps,
  };
  if (distinctFormats.length === 1) solution.format = distinctFormats[0]!;

  // --- 来源证据 ---
  const evidence: Evidence = {
    limits: {},
    maskedFields: {},
    auditTags: {},
    steps: steps.map((step, position) => {
      const owner = stepObli.find((s) => s.steps.includes(step));
      return { step, source: owner?.source ?? 'unknown', position };
    }),
    dependencies: deps.map((d) => ({
      key: d.key, relativeTo: d.relativeTo, minGap: d.minGap, source: d.source,
    })),
  };

  for (const [key, value] of Object.entries(limitSolution)) {
    const contributors = [...new Set(limits.filter((l) => l.key === key).map((l) => l.source))];
    const bound = bounds[key]!;
    const decidedBy = limits
      .filter((l) => l.key === key && l.value === value)
      .map((l) => l.source);
    const entry: { value: number; lo?: number; hi?: number; contributors: string[]; decidedBy: string[] } = {
      value,
      contributors,
      decidedBy: [...new Set(decidedBy)],
    };
    if (bound.lo !== undefined) entry.lo = bound.lo;
    if (bound.hi !== undefined) entry.hi = bound.hi;
    evidence.limits[key] = entry;
  }
  for (const field of maskedFields) {
    evidence.maskedFields[field] = [
      ...new Set(masks.filter((m) => m.fields.includes(field)).map((m) => m.source)),
    ];
  }
  for (const tag of auditTags) {
    evidence.auditTags[tag] = [
      ...new Set(audits.filter((a) => a.tags.includes(tag)).map((a) => a.source)),
    ];
  }
  if (distinctFormats.length === 1) {
    evidence.format = {
      value: distinctFormats[0]!,
      sources: [...new Set(formats.map((f) => f.source))],
    };
  }

  return { status: 'ok', solution, evidence };
}
