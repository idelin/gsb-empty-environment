import { AttributeStore } from './attribute-store.ts';
import { DecisionCache, InMemoryCacheStore } from './cache.ts';
import type { CacheStore, Decision } from './cache.ts';
import { evaluatePolicy } from './evaluator.ts';
import type { Policy } from './policy.ts';
import { validatePolicy } from './policy.ts';

export interface CheckResult {
  decision: Decision;
  /** true 表示直接来自缓存，未重新求值 */
  fromCache: boolean;
  /** 产生该结果的规则版本 */
  ruleVersion: number;
}

export interface PepOptions {
  store?: CacheStore;
  /** 注入时钟，便于测试时间失效点 */
  now?: () => number;
}

/**
 * 策略执行点：本地求值 + 判定缓存。
 * 属性仅来自进程内 AttributeStore，不接外部身份服务。
 */
export class PolicyEnforcementPoint {
  private policy: Policy;
  readonly attributes: AttributeStore;
  readonly cache: DecisionCache;
  private readonly now: () => number;

  constructor(policy: Policy, options: PepOptions = {}) {
    validatePolicy(policy);
    this.policy = policy;
    this.now = options.now ?? (() => Date.now());
    this.attributes = new AttributeStore(policy.attributes);
    this.cache = new DecisionCache(
      options.store ?? new InMemoryCacheStore(),
      this.attributes,
      policy.version,
      this.now,
    );
    this.cache.bind(this.attributes);
  }

  /** 同一主体同一上下文：命中缓存直接返回，否则本地求值并写入缓存 */
  check(subjectId: string, contextId: string): CheckResult {
    const key = DecisionCache.keyOf(subjectId, contextId);
    const hit = this.cache.get(key);
    if (hit) {
      return { decision: hit.decision, fromCache: true, ruleVersion: hit.ruleVersion };
    }
    const outcome = evaluatePolicy(this.policy, subjectId, this.attributes);
    // 默认拒绝（未命中规则）也缓存，使用 60 秒兜底 TTL；命中规则则用规则的 ttlSeconds
    const ttlSeconds = outcome.matchedRule ? outcome.matchedRule.ttlSeconds : 60;
    this.cache.put(key, {
      decision: outcome.decision,
      reads: outcome.reads,
      ruleVersion: this.policy.version,
      expiresAt: this.now() + ttlSeconds * 1000,
    });
    return { decision: outcome.decision, fromCache: false, ruleVersion: this.policy.version };
  }

  /** 规则版本变化：切换策略并丢弃全部旧版本结果（含允许） */
  setPolicy(next: Policy): void {
    validatePolicy(next);
    this.policy = next;
    this.cache.onRuleVersionChange(next.version);
  }

  get policyVersion(): number {
    return this.policy.version;
  }
}
