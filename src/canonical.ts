import type { EngineResult } from './types.ts';

/** 稳定序列化：对象键递归排序，使结果与插入顺序无关。 */
export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'undefined';
  if (Array.isArray(value)) return `[${value.map(canonicalize).join(',')}]`;
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, v]) => v !== undefined)
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([k, v]) => `${JSON.stringify(k)}:${canonicalize(v)}`);
  return `{${entries.join(',')}}`;
}

/**
 * 规范化引擎结果：候选按 ruleId 排序、冲突列表按字段排序，
 * 使两份语义相同但遍历顺序不同的结果可逐字节比较。
 */
export function normalizeResult(result: EngineResult): string {
  const evidence = Object.fromEntries(
    Object.entries(result.evidence).map(([field, ev]) => [
      field,
      {
        ...ev,
        conflict: ev.conflict
          ? { ...ev.conflict, ruleIds: [...ev.conflict.ruleIds].sort(), values: [...ev.conflict.values].sort() }
          : undefined,
        candidates: [...ev.candidates].map((c) => ({
          ...c,
          // 条件轨迹是树，本身与规则顺序无关，直接规范化即可。
          trace: c.trace,
        })).sort((a, b) => a.ruleId.localeCompare(b.ruleId)),
      },
    ]),
  );
  const conflicts = [...result.conflicts]
    .map((c) => ({ ...c, ruleIds: [...c.ruleIds].sort(), values: [...c.values].sort() }))
    .sort((a, b) => a.field.localeCompare(b.field));
  return canonicalize({ facts: result.facts, conflicts, evidence });
}
