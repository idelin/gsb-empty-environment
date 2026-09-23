import { RestoreError } from './errors.ts';
import type { Registry } from './registry.ts';

export interface StateRecord {
  readonly kind: 'state';
  readonly id: string;
  readonly value: unknown;
}

export interface ComputedRecord {
  readonly kind: 'computed';
  readonly id: string;
  readonly defId: string;
  readonly defVersion: number;
  readonly deps: readonly string[];
  /** 单调计算节点跨提交保留的上一值（求值不动点证据）。 */
  readonly carried?: unknown;
  value: unknown;
  dirty: boolean;
}

export type NodeRecord = StateRecord | ComputedRecord;

export type RecordMap = Map<string, NodeRecord>;

/** 检查一：共享身份 —— 同一 id 不得出现两次，所有引用共享同一节点。 */
export function checkSharedIdentity(ids: readonly string[]): void {
  const seen = new Set<string>();
  for (const id of ids) {
    if (seen.has(id)) {
      throw new RestoreError('DUPLICATE_NODE', `duplicate node id: ${id}`);
    }
    seen.add(id);
  }
}

/** 检查二：单调环 —— 自反馈边只允许出现在 monotonic 定义上。 */
export function checkMonotonicCycles(records: RecordMap, registry: Registry): void {
  for (const rec of records.values()) {
    if (rec.kind !== 'computed') continue;
    if (!rec.deps.includes(rec.id)) continue;
    const def = registry.get(rec.defId, rec.defVersion);
    if (!def?.monotonic) {
      throw new RestoreError(
        'MONOTONIC_CYCLE',
        `self-feedback edge on non-monotonic definition: ${rec.id} (${rec.defId}@${rec.defVersion})`,
      );
    }
  }
}

/** 检查三：非法求值环 —— 去掉合法单调自边后，计算节点间不得成环。 */
export function checkEvaluationCycles(records: RecordMap): void {
  const computedDeps = new Map<string, string[]>();
  for (const rec of records.values()) {
    if (rec.kind !== 'computed') continue;
    computedDeps.set(
      rec.id,
      rec.deps.filter((d) => d !== rec.id && computedOrUndefined(records, d)),
    );
  }
  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Map<string, number>();
  const stack: string[] = [];

  const visit = (id: string): void => {
    color.set(id, GRAY);
    stack.push(id);
    for (const dep of computedDeps.get(id) ?? []) {
      const c = color.get(dep) ?? WHITE;
      if (c === GRAY) {
        const cycle = [...stack.slice(stack.indexOf(dep)), dep].join(' -> ');
        throw new RestoreError('ILLEGAL_CYCLE', `illegal evaluation cycle: ${cycle}`);
      }
      if (c === WHITE) visit(dep);
    }
    stack.pop();
    color.set(id, BLACK);
  };

  for (const id of computedDeps.keys()) {
    if ((color.get(id) ?? WHITE) === WHITE) visit(id);
  }
}

function computedOrUndefined(records: RecordMap, id: string): string | undefined {
  const rec = records.get(id);
  return rec && rec.kind === 'computed' ? id : undefined;
}

/** 依赖节点存在性检查。 */
export function checkMissingDeps(records: RecordMap): void {
  for (const rec of records.values()) {
    if (rec.kind !== 'computed') continue;
    for (const dep of rec.deps) {
      if (!records.has(dep)) {
        throw new RestoreError(
          'MISSING_NODE',
          `computed node ${rec.id} depends on missing node: ${dep}`,
        );
      }
    }
  }
}

/**
 * 以拓扑序重算所有 dirty 计算节点。
 * 单调自边的 dep 值取上一快照中该节点的值（首轮取 def.initial）。
 */
export function recomputeAll(records: RecordMap, prev: RecordMap, registry: Registry): void {
  const done = new Set<string>();
  const visiting = new Set<string>();

  const evalNode = (id: string): unknown => {
    const rec = records.get(id);
    if (!rec) throw new RestoreError('UNKNOWN_NODE', `unknown node: ${id}`);
    if (rec.kind === 'state') return rec.value;
    if (!rec.dirty) {
      done.add(id);
      return rec.value;
    }
    if (visiting.has(id)) {
      throw new RestoreError('ILLEGAL_CYCLE', `evaluation cycle reached at: ${id}`);
    }
    visiting.add(id);
    const def = registry.get(rec.defId, rec.defVersion);
    if (!def) {
      throw new RestoreError('UNKNOWN_DEFINITION', `unknown definition: ${rec.defId}@${rec.defVersion}`);
    }
    const depValues = rec.deps.map((dep) => {
      if (dep === id) {
        const prevRec = prev.get(id);
        if (prevRec && prevRec.kind === 'computed' && prevRec.value !== undefined) {
          return prevRec.value;
        }
        if (rec.carried !== undefined) return rec.carried;
        return def.initial;
      }
      return evalNode(dep);
    });
    rec.value = def.compute(depValues);
    rec.dirty = false;
    visiting.delete(id);
    done.add(id);
    return rec.value;
  };

  for (const rec of records.values()) {
    if (rec.kind === 'computed') evalNode(rec.id);
  }
}

/** 从 changed 状态节点出发，把受影响的计算节点标脏。 */
export function markDirtyDownstream(records: RecordMap, changed: ReadonlySet<string>): void {
  const dependents = new Map<string, string[]>();
  for (const rec of records.values()) {
    if (rec.kind !== 'computed') continue;
    for (const dep of rec.deps) {
      if (dep === rec.id) continue;
      const list = dependents.get(dep) ?? [];
      list.push(rec.id);
      dependents.set(dep, list);
    }
  }
  const queue = [...changed];
  const seen = new Set<string>();
  while (queue.length > 0) {
    const id = queue.pop() as string;
    for (const next of dependents.get(id) ?? []) {
      if (seen.has(next)) continue;
      seen.add(next);
      const rec = records.get(next);
      if (rec && rec.kind === 'computed') rec.dirty = true;
      queue.push(next);
    }
  }
}
