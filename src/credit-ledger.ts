/**
 * 追溯性信用额度占用流水账（Retroactive Credit Ledger）
 *
 * 设计原则：
 * - 只追加、不删除：所有变更（包括冲正）都是新的流水记录，历史记录永不物理删除。
 * - 双时间轴：业务时间（time/from/to）表示事件生效的历史时刻；
 *   记录时间（recordedAt/seq）表示事件被写入账本的顺序。
 * - 违约检测：任意事件入账后，基于全量流水重算时间轴，
 *   识别“占用（含冻结） > 有效额度”的时间点并写入审计日志。
 *
 * 区间约定：所有期间区间均为左闭右开 [from, to)。
 */

export type LedgerEventType =
  | 'CONSUME' // 消费（占用额度）
  | 'RELEASE' // 释放（归还额度）
  | 'FREEZE' // 期间冻结（重叠期冻结可叠加）
  | 'LIMIT_ADJUST' // 额度调整（追溯生效，支持临时额度/调低额度）
  | 'REVERSAL'; // 冲正（逻辑抵消目标记录，不物理删除）

export type LimitAdjustMode = 'set' | 'delta';

export interface LedgerEvent {
  /** 全局唯一事件 ID */
  id: string;
  /** 入账顺序号（单调递增） */
  seq: number;
  /** 入账时间（逻辑时钟，可显式指定以表达“在历史时间点入账”） */
  recordedAt: number;
  type: LedgerEventType;
  /** CONSUME / RELEASE 的业务时间点 */
  time?: number;
  /** FREEZE / LIMIT_ADJUST 的期间起点（含） */
  from?: number;
  /** FREEZE / LIMIT_ADJUST 的期间终点（不含） */
  to?: number;
  /** 数值：消费/释放/冻结金额；LIMIT_ADJUST 的设定值或增减量 */
  amount?: number;
  /** LIMIT_ADJUST 的调整方式：set=期间内设定为该值；delta=期间内在基准上增减 */
  mode?: LimitAdjustMode;
  /** REVERSAL 指向的目标事件 ID */
  targetId?: string;
  reason?: string;
}

export interface Violation {
  /** 违约时间点 */
  time: number;
  /** 该时刻总占用（消费净额 + 冻结） */
  usage: number;
  /** 该时刻有效额度 */
  limit: number;
  /** 超额差量 = usage - limit */
  excess: number;
}

export interface AuditEntry {
  seq: number;
  recordedAt: number;
  /** 触发本次重算的入账事件 */
  triggerEventId: string;
  triggerEventSeq: number;
  status: 'ALERT' | 'RESOLVED';
  violations: Violation[];
}

export interface EventOptions {
  id?: string;
  recordedAt?: number;
  reason?: string;
}

export interface LimitAdjustOptions extends EventOptions {
  from: number;
  to: number;
  mode: LimitAdjustMode;
  value: number;
}

export class CreditLedger {
  readonly initialLimit: number;
  private readonly _events: LedgerEvent[] = [];
  private readonly _auditLog: AuditEntry[] = [];
  private seqCounter = 0;
  private logicalClock = 0;
  private lastViolationSignature = '[]';

  constructor(initialLimit: number) {
    if (!(initialLimit >= 0)) {
      throw new Error('initialLimit must be >= 0');
    }
    this.initialLimit = initialLimit;
  }

  /** 全量流水（只读副本，含已被冲正的记录——永不物理删除） */
  events(): readonly LedgerEvent[] {
    return this._events.map((e) => ({ ...e }));
  }

  /** 审计日志（只读副本） */
  auditLog(): readonly AuditEntry[] {
    return this._auditLog.map((e) => ({
      ...e,
      violations: e.violations.map((v) => ({ ...v })),
    }));
  }

  /** 消费：在业务时间 time 占用 amount 额度 */
  consume(time: number, amount: number, opts: EventOptions = {}): LedgerEvent {
    this.assertPositiveAmount(amount);
    return this.append({ type: 'CONSUME', time, amount }, opts);
  }

  /** 释放：在业务时间 time 归还 amount 额度 */
  release(time: number, amount: number, opts: EventOptions = {}): LedgerEvent {
    this.assertPositiveAmount(amount);
    return this.append({ type: 'RELEASE', time, amount }, opts);
  }

  /** 期间冻结：[from, to) 内冻结 amount；重叠期冻结效果叠加 */
  freeze(from: number, to: number, amount: number, opts: EventOptions = {}): LedgerEvent {
    this.assertPositiveAmount(amount);
    this.assertPeriod(from, to);
    return this.append({ type: 'FREEZE', from, to, amount }, opts);
  }

  /**
   * 追溯性额度调整：将历史期间 [from, to) 的额度
   * 设定为 value（mode='set'）或在基准上增减 value（mode='delta'，可正可负）。
   * 可用于在历史时间点插入临时额度或调低额度。
   */
  adjustLimit(options: LimitAdjustOptions): LedgerEvent {
    const { from, to, mode, value } = options;
    this.assertPeriod(from, to);
    if (mode === 'set' && !(value >= 0)) {
      throw new Error('limit value must be >= 0');
    }
    return this.append(
      { type: 'LIMIT_ADJUST', from, to, amount: value, mode },
      { id: options.id, recordedAt: options.recordedAt, reason: options.reason },
    );
  }

  /**
   * 历史冲正：追加一条冲正记录，逻辑抵消目标事件（含其历史效果）。
   * 目标记录保留在账本中，不被物理删除；对冲正记录再冲正可恢复原效果。
   */
  reverse(targetId: string, opts: EventOptions = {}): LedgerEvent {
    const target = this._events.find((e) => e.id === targetId);
    if (!target) {
      throw new Error(`cannot reverse unknown event: ${targetId}`);
    }
    return this.append({ type: 'REVERSAL', targetId }, opts);
  }

  /** 当前有效（未被冲正）的事件集合 */
  private activeEvents(): LedgerEvent[] {
    // 逆序扫描：事件是否被冲正，取决于指向它的、自身仍然有效的冲正记录数（奇偶）。
    // 冲正记录总是指向更早入账的事件，因此从后往前扫描可一次确定所有抵消关系。
    const cancelled = new Set<string>();
    for (let i = this._events.length - 1; i >= 0; i--) {
      const e = this._events[i];
      if (cancelled.has(e.id)) continue; // 被冲正的事件（含冲正记录本身）不再生效
      if (e.type === 'REVERSAL') {
        if (cancelled.has(e.targetId!)) {
          cancelled.delete(e.targetId!); // 冲正之冲正 = 恢复
        } else {
          cancelled.add(e.targetId!);
        }
      }
    }
    return this._events.filter((e) => e.type !== 'REVERSAL' && !cancelled.has(e.id));
  }

  /** 业务时间 t 的有效额度 */
  limitAt(t: number): number {
    let limit = this.initialLimit;
    for (const e of this.activeEvents()) {
      if (e.type !== 'LIMIT_ADJUST') continue;
      if (e.from! <= t && t < e.to!) {
        limit = e.mode === 'set' ? e.amount! : limit + e.amount!;
      }
    }
    return limit;
  }

  /** 业务时间 t 的总占用 = 消费净额 + 生效中的冻结（重叠冻结叠加） */
  usageAt(t: number): number {
    let usage = 0;
    for (const e of this.activeEvents()) {
      if (e.type === 'CONSUME' && e.time! <= t) usage += e.amount!;
      else if (e.type === 'RELEASE' && e.time! <= t) usage -= e.amount!;
      else if (e.type === 'FREEZE' && e.from! <= t && t < e.to!) usage += e.amount!;
    }
    return usage;
  }

  /**
   * 全时间轴违约扫描：占用与额度均为阶梯函数，
   * 只需在所有变化点求值即可覆盖全部违约时刻。
   */
  detectViolations(): Violation[] {
    const points = new Set<number>();
    for (const e of this.activeEvents()) {
      if (e.type === 'CONSUME' || e.type === 'RELEASE') {
        points.add(e.time!);
      } else {
        points.add(e.from!);
        points.add(e.to!);
      }
    }
    const violations: Violation[] = [];
    for (const p of [...points].sort((a, b) => a - b)) {
      const usage = this.usageAt(p);
      const limit = this.limitAt(p);
      if (usage > limit) {
        violations.push({ time: p, usage, limit, excess: usage - limit });
      }
    }
    return violations;
  }

  private append(
    draft: Omit<LedgerEvent, 'id' | 'seq' | 'recordedAt'>,
    opts: EventOptions,
  ): LedgerEvent {
    const seq = ++this.seqCounter;
    const recordedAt = opts.recordedAt ?? ++this.logicalClock;
    if (recordedAt > this.logicalClock) this.logicalClock = recordedAt;
    const event: LedgerEvent = {
      id: opts.id ?? `evt-${seq}`,
      seq,
      recordedAt,
      ...draft,
    };
    this._events.push(event);
    this.audit(event);
    return { ...event };
  }

  /** 每次入账后重算违约；违约集合发生变化时写入审计日志 */
  private audit(trigger: LedgerEvent): void {
    const violations = this.detectViolations();
    const signature = JSON.stringify(violations);
    if (signature === this.lastViolationSignature) return;
    this.lastViolationSignature = signature;
    this._auditLog.push({
      seq: this._auditLog.length + 1,
      recordedAt: trigger.recordedAt,
      triggerEventId: trigger.id,
      triggerEventSeq: trigger.seq,
      status: violations.length > 0 ? 'ALERT' : 'RESOLVED',
      violations,
    });
  }

  private assertPositiveAmount(amount: number): void {
    if (!(amount > 0)) throw new Error('amount must be > 0');
  }

  private assertPeriod(from: number, to: number): void {
    if (!(from < to)) throw new Error('period must satisfy from < to');
  }
}
