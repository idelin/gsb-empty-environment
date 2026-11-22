import type { Clock } from './clock.ts';
import { DenyScope, RuleId, type Evidence } from './types.ts';

interface LockEntry {
  scope: DenyScope;
  subject: string;
  lockedUntil: number;
  rule: RuleId;
  reason: string;
  evidence: Evidence;
  at: number;
}

/**
 * 锁定注册表：维护来源 / 账号 / 全局三类带 TTL 的锁定。
 * 已过期的锁在查询时惰性清除。
 */
export class LockRegistry {
  private readonly entries = new Map<string, LockEntry>();

  constructor(private readonly clock: Clock) {}

  private key(scope: string, subject: string): string {
    return `${scope}:${subject}`;
  }

  /** 新增或刷新一条锁定。 */
  lock(
    scope: DenyScope,
    subject: string,
    ttlMs: number,
    rule: RuleId,
    reason: string,
    evidence: Evidence,
  ): LockEntry {
    const now = this.clock.now();
    const entry: LockEntry = {
      scope,
      subject,
      lockedUntil: now + ttlMs,
      rule,
      reason,
      evidence,
      at: now,
    };
    this.entries.set(this.key(scope, subject), entry);
    return entry;
  }

  /** 查询当前生效的锁；过期则清除并返回 null。 */
  get(scope: DenyScope, subject: string): LockEntry | null {
    const mapKey = this.key(scope, subject);
    const entry = this.entries.get(mapKey);
    if (!entry) {
      return null;
    }
    if (this.clock.now() >= entry.lockedUntil) {
      this.entries.delete(mapKey);
      return null;
    }
    return entry;
  }

  /** 手动解除锁定（测试或运维使用）。 */
  unlock(scope: DenyScope, subject: string): void {
    this.entries.delete(this.key(scope, subject));
  }

  /** 当前所有生效锁的快照。 */
  snapshot(): LockEntry[] {
    const now = this.clock.now();
    const active: LockEntry[] = [];
    for (const [mapKey, entry] of this.entries) {
      if (now >= entry.lockedUntil) {
        this.entries.delete(mapKey);
        continue;
      }
      active.push({ ...entry, evidence: { ...entry.evidence } });
    }
    return active;
  }
}
