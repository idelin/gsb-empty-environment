import { Certificate } from "./certificate.ts";
import type { Interval } from "./interval.ts";
import { interval, overlaps, subtract } from "./interval.ts";

/** 班次：某员工在某时段内需要某技能资质。 */
export interface Shift {
  id: string;
  employeeId: string;
  requiredSkill: string;
  span: Interval;
}

export interface ScheduleAccepted {
  ok: true;
  shift: Shift;
}

export interface ScheduleRejected {
  ok: false;
  shift: Shift;
  /** 班次中缺乏资质覆盖的子区间。 */
  uncovered: Interval[];
}

export type ScheduleResult = ScheduleAccepted | ScheduleRejected;

/** 追溯吊销导致的违规预警。 */
export interface ViolationWarning {
  shiftId: string;
  employeeId: string;
  requiredSkill: string;
  certificateId: string;
  revokedAt: number;
  knownAt: number;
  /** 班次中因吊销而失去资质覆盖的子区间。 */
  violated: Interval[];
}

/**
 * 排班-资质双时态匹配器。
 * 排班时按当前知悉（事务时间）校验班次全程资质持续有效；
 * 收到追溯吊销通知后，自动扫描已排班次并输出违规预警列表。
 */
export class Scheduler {
  private certificates = new Map<string, Certificate>();
  private shifts: Shift[] = [];
  /** 系统当前事务时间（知悉时间），随事件推进。 */
  private clock = 0;

  advanceClock(to: number): void {
    if (to < this.clock) throw new RangeError("事务时间不可回退");
    this.clock = to;
  }

  addCertificate(cert: Certificate): void {
    this.certificates.set(cert.id, cert);
  }

  /** 计算班次区间内未被资质覆盖的部分（默认按当前知悉时间）。 */
  uncoveredIntervals(shift: Shift, knownAt: number = this.clock): Interval[] {
    const covers: Interval[] = [];
    for (const cert of this.certificates.values()) {
      if (cert.employeeId !== shift.employeeId || cert.skill !== shift.requiredSkill) continue;
      const eff = cert.effectiveInterval(knownAt);
      if (eff) covers.push(eff);
    }
    return subtract(shift.span, covers);
  }

  /** 尝试排班：班次全程被有效资质覆盖才接受，否则拒绝并给出缺口。 */
  schedule(shift: Shift): ScheduleResult {
    interval(shift.span.start, shift.span.end);
    const uncovered = this.uncoveredIntervals(shift);
    if (uncovered.length > 0) return { ok: false, shift, uncovered };
    this.shifts.push(shift);
    return { ok: true, shift };
  }

  scheduledShifts(): readonly Shift[] {
    return this.shifts;
  }

  /**
   * 登记追溯吊销，并自动扫描该员工该技能已排班次，
   * 输出因吊销而失去覆盖的违规预警列表。
   */
  revoke(certificateId: string, revokedAt: number, knownAt: number): ViolationWarning[] {
    const cert = this.certificates.get(certificateId);
    if (!cert) throw new Error(`未知证书：${certificateId}`);
    if (knownAt < this.clock) throw new RangeError("知悉时间早于系统时钟");
    this.clock = knownAt;
    cert.revoke(revokedAt, knownAt);

    const warnings: ViolationWarning[] = [];
    for (const shift of this.shifts) {
      if (shift.employeeId !== cert.employeeId || shift.requiredSkill !== cert.skill) continue;
      // 只关心与吊销影响区 [revokedAt, +∞) 相交的班次
      if (!overlaps(shift.span, { start: revokedAt, end: Number.POSITIVE_INFINITY })) continue;
      const violated = this.uncoveredIntervals(shift, knownAt);
      if (violated.length > 0) {
        warnings.push({
          shiftId: shift.id,
          employeeId: shift.employeeId,
          requiredSkill: shift.requiredSkill,
          certificateId: cert.id,
          revokedAt,
          knownAt,
          violated,
        });
      }
    }
    return warnings;
  }
}
