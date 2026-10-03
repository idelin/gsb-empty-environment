/**
 * 追溯性信用额度占用流水账
 *
 * 设计原则：
 * - 只追加、不物理删除：所有消费、额度调整、冻结、违约警报均以不可变记录追加到账本。
 * - 历史冲正：额度调整可指定任意生效区间 [from, to]，包括过去的时间点。
 * - 重叠期冻结：被冻结的区间拒绝任何与之重叠的额度调整，拒绝行为本身也记入审计日志。
 * - 违约识别：每次变更后重放全部消费流水，识别占用超过当时额度的时刻并输出警报。
 */

export interface ConsumptionRecord {
  readonly seq: number;
  readonly time: number;
  readonly amount: number;
}

export interface LimitAdjustment {
  readonly seq: number;
  readonly from: number;
  readonly to: number;
  readonly limit: number;
  /** 记录入账的时间（可与生效区间不同，体现"追溯"） */
  readonly recordedAt: number;
}

export interface FrozenRange {
  readonly seq: number;
  readonly from: number;
  readonly to: number;
  readonly reason: string;
}

export interface Violation {
  readonly time: number;
  readonly usage: number;
  readonly limit: number;
  readonly excess: number;
}

export type AuditAction =
  | 'INIT'
  | 'CONSUME'
  | 'ADJUST_LIMIT'
  | 'ADJUST_REJECTED_FROZEN'
  | 'FREEZE'
  | 'VIOLATION_ALERT';

export interface AuditEntry {
  readonly seq: number;
  readonly action: AuditAction;
  readonly detail: string;
  readonly payload?: unknown;
}

export type AdjustResult =
  | { ok: true; adjustment: LimitAdjustment; newViolations: Violation[] }
  | { ok: false; reason: string };

export class CreditLedger {
  private readonly baseLimit: number;
  private seq = 0;
  private readonly consumptions: ConsumptionRecord[] = [];
  private readonly adjustments: LimitAdjustment[] = [];
  private readonly frozenRanges: FrozenRange[] = [];
  private readonly violations: Violation[] = [];
  private readonly auditLog: AuditEntry[] = [];

  constructor(baseLimit: number) {
    this.baseLimit = baseLimit;
    this.audit('INIT', `初始额度 ${baseLimit}`);
  }

  /** 在指定时间点登记一笔消费（占用额度）。 */
  consume(time: number, amount: number): void {
    if (amount <= 0) throw new Error('消费金额必须为正数');
    const record: ConsumptionRecord = { seq: ++this.seq, time, amount };
    this.consumptions.push(record);
    this.consumptions.sort((a, b) => a.time - b.time || a.seq - b.seq);
    this.audit('CONSUME', `时间 ${time} 消费 ${amount}`, record);
    this.recomputeViolations();
  }

  /**
   * 追溯调整额度：将区间 [from, to] 内的额度设为 newLimit。
   * 与冻结区间重叠时拒绝（不物理删除任何记录，拒绝本身写入审计日志）。
   */
  adjustLimit(from: number, to: number, newLimit: number, recordedAt: number): AdjustResult {
    if (from > to) throw new Error('生效区间起点不能大于终点');
    if (newLimit < 0) throw new Error('额度不能为负数');

    const conflict = this.frozenRanges.find((f) => f.from <= to && from <= f.to);
    if (conflict) {
      const reason = `区间 [${from}, ${to}] 与冻结区间 [${conflict.from}, ${conflict.to}]（${conflict.reason}）重叠，调整被拒绝`;
      this.audit('ADJUST_REJECTED_FROZEN', reason, { from, to, newLimit, recordedAt });
      return { ok: false, reason };
    }

    const adjustment: LimitAdjustment = {
      seq: ++this.seq,
      from,
      to,
      limit: newLimit,
      recordedAt,
    };
    this.adjustments.push(adjustment);
    this.audit(
      'ADJUST_LIMIT',
      `时间 ${recordedAt} 追溯调整区间 [${from}, ${to}] 额度为 ${newLimit}`,
      adjustment,
    );
    const newViolations = this.recomputeViolations();
    return { ok: true, adjustment, newViolations };
  }

  /** 冻结区间：此后任何与之重叠的额度调整都会被拒绝。 */
  freeze(from: number, to: number, reason = '未说明'): void {
    if (from > to) throw new Error('冻结区间起点不能大于终点');
    const range: FrozenRange = { seq: ++this.seq, from, to, reason };
    this.frozenRanges.push(range);
    this.audit('FREEZE', `冻结区间 [${from}, ${to}]：${reason}`, range);
  }

  /** 查询某一时刻的有效额度：覆盖该时刻的调整中，最晚入账者生效。 */
  getLimitAt(time: number): number {
    let limit = this.baseLimit;
    let latestSeq = -1;
    for (const adj of this.adjustments) {
      if (adj.from <= time && time <= adj.to && adj.seq > latestSeq) {
        latestSeq = adj.seq;
        limit = adj.limit;
      }
    }
    return limit;
  }

  /** 查询某一时刻的累计占用（该时刻及之前的全部消费）。 */
  getUsageAt(time: number): number {
    return this.consumptions
      .filter((c) => c.time <= time)
      .reduce((sum, c) => sum + c.amount, 0);
  }

  getViolations(): readonly Violation[] {
    return this.violations;
  }

  getAuditLog(): readonly AuditEntry[] {
    return this.auditLog;
  }

  /**
   * 重放全部消费流水，识别每个消费时刻占用是否超过当时额度。
   * 只追加新发现的违约警报，已记录的警报永不删除。
   */
  private recomputeViolations(): Violation[] {
    const known = new Set(this.violations.map((v) => v.time));
    const fresh: Violation[] = [];
    let usage = 0;
    for (const c of this.consumptions) {
      usage += c.amount;
      const limit = this.getLimitAt(c.time);
      if (usage > limit && !known.has(c.time)) {
        const violation: Violation = {
          time: c.time,
          usage,
          limit,
          excess: usage - limit,
        };
        this.violations.push(violation);
        fresh.push(violation);
        this.audit(
          'VIOLATION_ALERT',
          `违约警报：时间 ${c.time} 占用 ${usage} 超过额度 ${limit}，超额 ${violation.excess}`,
          violation,
        );
      }
    }
    return fresh;
  }

  private audit(action: AuditAction, detail: string, payload?: unknown): void {
    this.auditLog.push({ seq: this.auditLog.length + 1, action, detail, payload });
  }
}
