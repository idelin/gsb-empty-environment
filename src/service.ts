import { AttributeStore, attrKey, type Scope } from './attributes.ts';
import { assertAttrValue, type AttrValue } from './conditions.ts';
import { DecisionCache, type CacheEntry } from './cache.ts';
import { evaluatePolicy, validatePolicy, type Decision, type Policy } from './engine.ts';

export interface AuthorizeResult {
  readonly decision: Decision;
  readonly cache: 'hit' | 'miss';
  /** miss 时返回新写入的缓存条目（含实际读取属性、规则版本、时间失效点）。 */
  readonly entry?: CacheEntry;
}

/**
 * 授权服务：属性仓库 + 策略引擎 + 判定缓存。
 * 失效语义：
 * - 属性更正：仅失效实际读取过该属性的条目；未读取的属性变化不失效任何条目；
 * - 已读取属性被删除：必须失效相关条目；删除本就不存在的属性不失效；
 * - 规则版本变化：该策略所有旧结果（含允许）立即作废；
 * - 时间失效点：条目过期后视为未命中。
 */
export class AuthorizationService {
  #policy: Policy;
  readonly #store = new AttributeStore();
  readonly #cache: DecisionCache;

  constructor(options: { policy: Policy; ttlMs?: number; now?: () => number }) {
    validatePolicy(options.policy);
    this.#policy = options.policy;
    this.#cache = new DecisionCache({ ttlMs: options.ttlMs ?? 60_000, now: options.now });
  }

  authorize(subjectId: string, contextId: string): AuthorizeResult {
    const cached = this.#cache.get(this.#policy.id, subjectId, contextId, this.#policy.version);
    if (cached) return { decision: cached.decision, cache: 'hit' };
    const { decision, reads } = evaluatePolicy(this.#policy, subjectId, contextId, this.#store);
    const entry = this.#cache.put(this.#policy.id, subjectId, contextId, decision, this.#policy.version, reads);
    return { decision, cache: 'miss', entry };
  }

  setSubjectAttribute(subjectId: string, name: string, value: AttrValue): void {
    this.#setAttribute('subject', subjectId, name, value);
  }

  setContextAttribute(contextId: string, name: string, value: AttrValue): void {
    this.#setAttribute('context', contextId, name, value);
  }

  deleteSubjectAttribute(subjectId: string, name: string): void {
    this.#deleteAttribute('subject', subjectId, name);
  }

  deleteContextAttribute(contextId: string, name: string): void {
    this.#deleteAttribute('context', contextId, name);
  }

  /** 更新策略：规则有变化就必须递增版本，旧版本结果一律作废。 */
  updatePolicy(next: Policy): void {
    validatePolicy(next);
    if (next.id !== this.#policy.id) {
      throw new Error(`不能用策略 "${next.id}" 替换策略 "${this.#policy.id}"`);
    }
    if (next.version === this.#policy.version) {
      throw new Error('规则发生变化时必须递增版本号，否则旧缓存结果可能被沿用');
    }
    this.#policy = next;
    this.#cache.invalidatePolicy(next.id);
  }

  get policyVersion(): number {
    return this.#policy.version;
  }

  get cacheSize(): number {
    return this.#cache.size;
  }

  #setAttribute(scope: Scope, ownerId: string, name: string, value: AttrValue): void {
    assertAttrValue(value, `set ${scope}.${name}`);
    const changed = this.#store.set(scope, ownerId, name, value);
    if (changed) this.#cache.invalidateAttribute(attrKey(scope, ownerId, name));
  }

  #deleteAttribute(scope: Scope, ownerId: string, name: string): void {
    const existed = this.#store.delete(scope, ownerId, name);
    if (existed) this.#cache.invalidateAttribute(attrKey(scope, ownerId, name));
  }
}
