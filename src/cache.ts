import type { AttributeChangeEvent, AttributeStore } from './attribute-store.ts';
import type { AttributeRead } from './evaluator.ts';

export type Decision = 'allow' | 'deny';

export interface CacheKey {
  subjectId: string;
  /** 上下文（如动作/资源）的不透明标识，参与缓存键，跨上下文不串用 */
  contextId: string;
}

export interface CacheEntry {
  decision: Decision;
  /** 判定该结果时实际读取的属性及读取时刻的版本 */
  reads: readonly AttributeRead[];
  /** 产生该结果的规则版本 */
  ruleVersion: number;
  /** 时间失效点（epoch 毫秒），超过即视为失效 */
  expiresAt: number;
}

export interface CacheStore {
  get(key: CacheKey): CacheEntry | undefined;
  set(key: CacheKey, entry: CacheEntry): void;
  delete(key: CacheKey): void;
  entries(): IterableIterator<[string, CacheEntry]>;
  clear(): void;
}

const KEY_SEPARATOR = String.fromCharCode(0);

export function serializeKey(key: CacheKey): string {
  return key.subjectId + KEY_SEPARATOR + key.contextId;
}

export class InMemoryCacheStore implements CacheStore {
  private readonly map = new Map<string, CacheEntry>();

  get(key: CacheKey): CacheEntry | undefined {
    return this.map.get(serializeKey(key));
  }

  set(key: CacheKey, entry: CacheEntry): void {
    this.map.set(serializeKey(key), entry);
  }

  delete(key: CacheKey): void {
    this.map.delete(serializeKey(key));
  }

  entries(): IterableIterator<[string, CacheEntry]> {
    return this.map.entries();
  }

  clear(): void {
    this.map.clear();
  }
}

export interface VersionProbe {
  readWithVersion(
    subjectId: string,
    attribute: string,
  ): { present: boolean; value: unknown; version: number };
}

/**
 * 判定结果缓存：
 * - 键 = 主体 + 上下文，跨主体/跨上下文天然隔离；
 * - 条目记录实际读取的属性版本、规则版本与时间失效点；
 * - 属性变更事件只失效“读取过该属性”的条目（读后删除同样失效）；
 * - 规则版本变化整体清空，旧允许结果不得沿用。
 */
export class DecisionCache {
  private readonly store: CacheStore;
  private readonly attributes: VersionProbe;
  private readonly now: () => number;
  private currentRuleVersion: number;

  constructor(
    store: CacheStore,
    attributes: VersionProbe,
    initialRuleVersion: number,
    now: () => number = () => Date.now(),
  ) {
    this.store = store;
    this.attributes = attributes;
    this.now = now;
    this.currentRuleVersion = initialRuleVersion;
  }

  static keyOf(subjectId: string, contextId: string): CacheKey {
    return { subjectId, contextId };
  }

  /** 命中返回条目；未命中/已失效返回 undefined（失效条目顺带清除） */
  get(key: CacheKey): CacheEntry | undefined {
    const entry = this.store.get(key);
    if (!entry) return undefined;
    if (entry.ruleVersion !== this.currentRuleVersion) {
      this.store.delete(key);
      return undefined;
    }
    if (this.now() >= entry.expiresAt) {
      this.store.delete(key);
      return undefined;
    }
    // 惰性兜底：即使漏掉了变更事件，版本比对也能发现已读属性被修改或删除
    for (const read of entry.reads) {
      const current = this.attributes.readWithVersion(key.subjectId, read.attribute);
      if (current.version !== read.version) {
        this.store.delete(key);
        return undefined;
      }
    }
    return entry;
  }

  put(key: CacheKey, entry: CacheEntry): void {
    this.store.set(key, entry);
  }

  /** 属性更正/删除：只失效读取过该属性的该主体条目，其余不受影响 */
  onAttributeChange(event: AttributeChangeEvent): void {
    const doomed: CacheKey[] = [];
    for (const [serialized, entry] of this.store.entries()) {
      const separatorAt = serialized.indexOf(KEY_SEPARATOR);
      const subjectId = serialized.slice(0, separatorAt);
      const contextId = serialized.slice(separatorAt + KEY_SEPARATOR.length);
      if (subjectId !== event.subjectId) continue;
      if (entry.reads.some((r) => r.attribute === event.attribute)) {
        doomed.push({ subjectId, contextId });
      }
    }
    for (const key of doomed) this.store.delete(key);
  }

  /** 订阅属性仓库的变更事件 */
  bind(store: AttributeStore): void {
    store.onChange((event) => this.onAttributeChange(event));
  }

  /** 规则版本变化：所有旧版本结果（包括允许）一律不得沿用 */
  onRuleVersionChange(newVersion: number): void {
    this.currentRuleVersion = newVersion;
    this.store.clear();
  }

  get ruleVersion(): number {
    return this.currentRuleVersion;
  }
}
