/**
 * 向量时钟（Vector Clock）基础工具。
 * 时钟表示为 { 节点ID: 逻辑计数 }，缺失的键视为 0。
 */

export type VectorClock = Record<string, number>;

export function get(clock: VectorClock, nodeId: string): number {
  return clock[nodeId] ?? 0;
}

/** 本地事件：将本节点分量加一，返回新时钟（不可变更新）。 */
export function tick(clock: VectorClock, nodeId: string): VectorClock {
  return { ...clock, [nodeId]: get(clock, nodeId) + 1 };
}

/** 合并两个时钟：逐键取最大值。 */
export function merge(a: VectorClock, b: VectorClock): VectorClock {
  const result: VectorClock = { ...a };
  for (const key of Object.keys(b)) {
    result[key] = Math.max(get(result, key), get(b, key));
  }
  return result;
}

/** a 是否发生在 b 之前（a <= b 且 a != b）。 */
export function happenedBefore(a: VectorClock, b: VectorClock): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  let strictlyLess = false;
  for (const key of keys) {
    if (get(a, key) > get(b, key)) return false;
    if (get(a, key) < get(b, key)) strictlyLess = true;
  }
  return strictlyLess;
}

export function format(clock: VectorClock): string {
  const keys = Object.keys(clock).sort();
  return `[${keys.map((k) => `${k}:${clock[k]}`).join(", ")}]`;
}
