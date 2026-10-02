import type { Interval } from "./interval.ts";
import { interval } from "./interval.ts";

/**
 * 追溯吊销记录（双时态）：
 * - revokedAt：有效时间（valid time），证书自该时刻起失效；
 * - knownAt：事务时间（transaction time），系统知悉该吊销的时刻。
 */
export interface Revocation {
  revokedAt: number;
  knownAt: number;
}

/**
 * 技能证书。持证有效期为 [validFrom, validTo)，
 * 可选复审期 reviewBy：若存在，则有效截止取 min(validTo, reviewBy)。
 * 吊销以事件形式追加，支持“事后知悉”的追溯吊销。
 */
export class Certificate {
  readonly id: string;
  readonly employeeId: string;
  readonly skill: string;
  readonly validFrom: number;
  readonly validTo: number;
  readonly reviewBy?: number;
  readonly revocations: Revocation[] = [];

  constructor(init: {
    id: string;
    employeeId: string;
    skill: string;
    validFrom: number;
    validTo: number;
    reviewBy?: number;
  }) {
    interval(init.validFrom, init.validTo);
    if (init.reviewBy !== undefined && init.reviewBy <= init.validFrom) {
      throw new RangeError("复审截止必须晚于生效起点");
    }
    this.id = init.id;
    this.employeeId = init.employeeId;
    this.skill = init.skill;
    this.validFrom = init.validFrom;
    this.validTo = init.validTo;
    this.reviewBy = init.reviewBy;
  }

  /** 原始有效截止（不含吊销）：min(validTo, reviewBy)。 */
  baseValidTo(): number {
    return this.reviewBy === undefined ? this.validTo : Math.min(this.validTo, this.reviewBy);
  }

  /**
   * 在指定知悉时间 knownAt 下，证书的有效区间（双时态快照）。
   * 只采纳 knownAt <= 查询时刻的吊销记录；取最早的吊销点截断。
   */
  effectiveInterval(knownAt: number): Interval | null {
    let end = this.baseValidTo();
    for (const r of this.revocations) {
      if (r.knownAt <= knownAt && r.revokedAt < end) end = r.revokedAt;
    }
    return this.validFrom < end ? { start: this.validFrom, end } : null;
  }

  revoke(revokedAt: number, knownAt: number): void {
    this.revocations.push({ revokedAt, knownAt });
  }
}
