/** 事件记录：seq 由服务端日志单调递增分配，全局唯一。 */
export interface EventRecord {
  seq: number;
  topic: string;
  key: string;
  value: unknown;
  ts: number;
}

/** 订阅条件。空数组表示不限制。 */
export interface Filter {
  topics?: string[];
  keys?: string[];
}

/** 规范化后的订阅条件（排序、去重、补齐默认值），用于稳定哈希。 */
export interface NormalizedFilter {
  topics: string[];
  keys: string[];
}

/** 快照中的一条状态条目。 */
export interface StateEntry {
  topic: string;
  key: string;
  value: unknown;
}

export function stateKey(topic: string, key: string): string {
  return `${topic}:${key}`;
}

export function normalizeFilter(filter: Filter): NormalizedFilter {
  return {
    topics: [...new Set(filter.topics ?? [])].sort(),
    keys: [...new Set(filter.keys ?? [])].sort(),
  };
}

export function matchesFilter(
  event: { topic: string; key: string },
  filter: Filter,
): boolean {
  const normalized = normalizeFilter(filter);
  if (normalized.topics.length > 0 && !normalized.topics.includes(event.topic)) {
    return false;
  }
  if (normalized.keys.length > 0 && !normalized.keys.includes(event.key)) {
    return false;
  }
  return true;
}
