import type { AttrValue } from './conditions.ts';
import type { Decision } from './engine.ts';

export interface CacheEntry {
  readonly policyId: string;
  readonly subjectId: string;
  readonly contextId: string;
  readonly decision: Decision;
  /** 判定所依据的规则版本。 */
  readonly ruleVersion: number;
  /** 判定时实际读取的属性快照，用于精确失效。 */
  readonly reads: ReadonlyMap<string, AttrValue | undefined>;
  /** 时间失效点（毫秒时间戳），到达后条目不可再用。 */
  readonly expiresAt: number;
}

/**
 * 判定缓存。
 * - 键为 (policyId, subjectId, contextId)：跨主体、跨上下文绝不串用；
 * - 每条记录携带规则版本与时间失效点；
 * - 维护 属性 -> 条目 的反向索引，属性更正/删除时只失效真正读过它的条目。
 */
export class DecisionCache {
  readonly #entries = new Map<string, CacheEntry>();
  readonly #byAttribute = new Map<string, Set<string>>();
  readonly #byPolicy = new Map<string, Set<string>>();
  readonly #now: () => number;
  readonly #ttlMs: number;

  constructor(options: { ttlMs: number; now?: () => number }) {
    if (!(options.ttlMs > 0)) throw new TypeError('ttlMs 必须是正数');
    this.#ttlMs = options.ttlMs;
    this.#now = options.now ?? Date.now;
  }

  static keyOf(policyId: string, subjectId: string, contextId: string): string {
    return `${policyId}${subjectId}${contextId}`;
  }

  /** 命中条件：条目存在、未过时间失效点、规则版本与当前一致。 */
  get(policyId: string, subjectId: string, contextId: string, ruleVersion: number): CacheEntry | undefined {
    const key = DecisionCache.keyOf(policyId, subjectId, contextId);
    const entry = this.#entries.get(key);
    if (!entry) return undefined;
    if (entry.expiresAt <= this.#now()) {
      this.#remove(key);
      return undefined;
    }
    if (entry.ruleVersion !== ruleVersion) return undefined;
    return entry;
  }

  put(
    policyId: string,
    subjectId: string,
    contextId: string,
    decision: Decision,
    ruleVersion: number,
    reads: ReadonlyMap<string, AttrValue | undefined>,
  ): CacheEntry {
    const key = DecisionCache.keyOf(policyId, subjectId, contextId);
    this.#remove(key);
    const entry: CacheEntry = {
      policyId,
      subjectId,
      contextId,
      decision,
      ruleVersion,
      reads: new Map(reads),
      expiresAt: this.#now() + this.#ttlMs,
    };
    this.#entries.set(key, entry);
    for (const attributeKey of entry.reads.keys()) {
      let bucket = this.#byAttribute.get(attributeKey);
      if (!bucket) this.#byAttribute.set(attributeKey, (bucket = new Set()));
      bucket.add(key);
    }
    let policyBucket = this.#byPolicy.get(policyId);
    if (!policyBucket) this.#byPolicy.set(policyId, (policyBucket = new Set()));
    policyBucket.add(key);
    return entry;
  }

  /** 属性更正或删除：只失效实际读取过该属性的条目。 */
  invalidateAttribute(attributeKey: string): number {
    const bucket = this.#byAttribute.get(attributeKey);
    if (!bucket) return 0;
    const keys = [...bucket];
    for (const key of keys) this.#remove(key);
    return keys.length;
  }

  /** 规则版本变化：该策略的全部旧结果（含允许）一律不可沿用。 */
  invalidatePolicy(policyId: string): number {
    const bucket = this.#byPolicy.get(policyId);
    if (!bucket) return 0;
    const keys = [...bucket];
    for (const key of keys) this.#remove(key);
    return keys.length;
  }

  get size(): number {
    return this.#entries.size;
  }

  #remove(key: string): void {
    const entry = this.#entries.get(key);
    if (!entry) return;
    this.#entries.delete(key);
    for (const attributeKey of entry.reads.keys()) {
      const bucket = this.#byAttribute.get(attributeKey);
      if (bucket) {
        bucket.delete(key);
        if (bucket.size === 0) this.#byAttribute.delete(attributeKey);
      }
    }
    const policyBucket = this.#byPolicy.get(entry.policyId);
    if (policyBucket) {
      policyBucket.delete(key);
      if (policyBucket.size === 0) this.#byPolicy.delete(entry.policyId);
    }
  }
}
