import { isDeepStrictEqual } from 'node:util';
import {
  covers,
  merge,
  mergeDot,
  dotKey,
  compareDots,
  type Dot,
  type VersionVector,
} from './version-vector.ts';

/**
 * 点状因果事件：一次写入 = 点状版本标识 (NodeID, Counter) + 前置向量。
 * 前置向量记录该事件发生时写入者已知的全部因果历史（不含自身的点）。
 */
export interface DottedEvent<V> {
  kind: 'dvv-event';
  key: string;
  dot: Dot;
  context: VersionVector;
  value: V;
}

export interface DottedValue<V> {
  dot: Dot;
  value: V;
}

/**
 * 单键的 DVV 状态（dvvset）：
 * - values: 活跃兄弟分支，每个分支由自己的点唯一标识；
 * - context: 已被后续写入覆盖/合并的全部事件，压缩为每节点一个计数器。
 * 不变式：任何活跃点都不被 context 覆盖；context 与活跃点之并即完整因果历史。
 */
export interface KeyState<V> {
  values: Array<DottedValue<V>>;
  context: VersionVector;
}

export function emptyState<V>(): KeyState<V> {
  return { values: [], context: {} };
}

export function isCovered<V>(state: KeyState<V>, dot: Dot): boolean {
  return (
    covers(state.context, dot) ||
    state.values.some((entry) => dotKey(entry.dot) === dotKey(dot))
  );
}

/** 应用一条点状事件（乱序/重复投递安全，幂等）。 */
export function applyEvent<V>(state: KeyState<V>, event: DottedEvent<V>): KeyState<V> {
  if (covers(state.context, event.dot)) return state;
  const duplicate = state.values.find(
    (entry) => dotKey(entry.dot) === dotKey(event.dot),
  );
  if (duplicate) {
    if (!isDeepStrictEqual(duplicate.value, event.value)) {
      throw new Error(`dot ${dotKey(event.dot)} reused with a different value`);
    }
    return state;
  }
  const survivors: Array<DottedValue<V>> = [];
  let context = merge(state.context, event.context);
  for (const entry of state.values) {
    if (covers(event.context, entry.dot)) {
      context = mergeDot(context, entry.dot);
    } else {
      survivors.push(entry);
    }
  }
  return dedupValues({
    values: [...survivors, { dot: event.dot, value: event.value }],
    context,
  });
}

/** 全量反熵合并两个副本的单键状态。 */
export function reconcile<V>(a: KeyState<V>, b: KeyState<V>): KeyState<V> {
  const kept: Array<DottedValue<V>> = [];
  let context = merge(a.context, b.context);
  for (const entry of a.values) {
    if (covers(b.context, entry.dot)) context = mergeDot(context, entry.dot);
    else kept.push(entry);
  }
  for (const entry of b.values) {
    if (covers(a.context, entry.dot)) context = mergeDot(context, entry.dot);
    else kept.push(entry);
  }
  const byDot = new Map<string, DottedValue<V>>();
  for (const entry of kept) {
    const key = dotKey(entry.dot);
    const existing = byDot.get(key);
    if (existing) {
      if (!isDeepStrictEqual(existing.value, entry.value)) {
        throw new Error(`dot ${key} reused with a different value`);
      }
    } else {
      byDot.set(key, entry);
    }
  }
  return dedupValues({ values: [...byDot.values()], context });
}

/**
 * 去重优化：值相同的并发/重复写入折叠为一个确定性的代表分支，
 * 被折叠分支的点并入前置向量，从而消除冗余兄弟与幽灵分支。
 */
export function dedupValues<V>(state: KeyState<V>): KeyState<V> {
  if (state.values.length < 2) return state;
  const groups = new Map<string, Array<DottedValue<V>>>();
  for (const entry of state.values) {
    const tag = stableStringify(entry.value);
    const group = groups.get(tag);
    if (group) group.push(entry);
    else groups.set(tag, [entry]);
  }
  const values: Array<DottedValue<V>> = [];
  let context = state.context;
  for (const group of groups.values()) {
    group.sort((x, y) => compareDots(x.dot, y.dot));
    const [representative, ...redundant] = group;
    values.push(representative);
    for (const entry of redundant) context = mergeDot(context, entry.dot);
  }
  values.sort((x, y) => compareDots(x.dot, y.dot));
  return { values, context };
}

/** 版本图体积：活跃点数 + 前置向量条目数。 */
export function versionGraphSize<V>(state: KeyState<V>): number {
  return state.values.length + Object.keys(state.context).length;
}

export function stableStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value) ?? 'undefined';
  }
  if (Array.isArray(value)) {
    return `[${value.map(stableStringify).join(',')}]`;
  }
  const entries = Object.entries(value as Record<string, unknown>)
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([k, v]) => `${JSON.stringify(k)}:${stableStringify(v)}`);
  return `{${entries.join(',')}}`;
}
