import { subtract, union } from "./intervals.ts";
import type { Interval } from "./intervals.ts";

/** 技能证书：validFrom/validTo 为生效期（valid time，半开区间）。 */
export interface Certificate {
  id: string;
  employeeId: string;
  skill: string;
  validFrom: number;
  validTo: number;
}

/** 班次：要求员工在 [start, end) 内持续持有 skill 资质。 */
export interface Shift {
  id: string;
  employeeId: string;
  skill: string;
  start: number;
  end: number;
}

/** 复审事件：知悉时间 knownAt 时确认证书有效期延长至 extendedTo。 */
export interface ReviewEvent {
  kind: "review";
  knownAt: number;
  certId: string;
  extendedTo: number;
}

/** 吊销事件：知悉时间 knownAt 时获悉证书在 revokedAt（valid time）被吊销。 */
export interface RevocationEvent {
  kind: "revocation";
  knownAt: number;
  certId: string;
  revokedAt: number;
}

export type KnowledgeEvent = ReviewEvent | RevocationEvent;

export interface ValidationResult {
  ok: boolean;
  /** 班次时间段内未被资质覆盖的缺口。 */
  missing: Interval[];
}

export interface ShiftViolation {
  shift: Shift;
  missing: Interval[];
  reason: string;
}

export interface RevocationReport {
  certId: string;
  revokedAt: number;
  knownAt: number;
  violations: ShiftViolation[];
}

/**
 * 双时态（valid time + knowledge time）排班资质匹配器。
 *
 * - valid time：证书在现实世界中生效 / 复审 / 吊销的时间。
 * - knowledge time（事务时间）：系统获悉某一事实的时间。
 * 所有判定均可在指定 knowledge time 上重放，支持追溯性吊销的影响分析。
 */
export class QualificationMatcher {
  private certs = new Map<string, Certificate>();
  private events: KnowledgeEvent[] = [];
  private scheduled: Shift[] = [];

  issueCertificate(cert: Certificate): void {
    if (this.certs.has(cert.id)) {
      throw new Error(`证书已存在: ${cert.id}`);
    }
    this.certs.set(cert.id, { ...cert });
  }

  /** 登记复审结果（知悉时间 knownAt，有效期延长至 extendedTo）。 */
  recordReview(certId: string, extendedTo: number, knownAt: number): void {
    this.requireCert(certId);
    this.events.push({ kind: "review", knownAt, certId, extendedTo });
  }

  /**
   * 登记吊销通知（知悉时间 knownAt 获悉，证书在 valid time revokedAt 被吊销），
   * 并返回受影响的已排班次违规报告。
   */
  recordRevocation(certId: string, revokedAt: number, knownAt: number): RevocationReport {
    const cert = this.requireCert(certId);
    this.events.push({ kind: "revocation", knownAt, certId, revokedAt });
    const violations = this.findViolations(
      (shift) => shift.employeeId === cert.employeeId && shift.skill === cert.skill,
      knownAt,
      `证书 ${certId} 于 ${revokedAt} 被追溯吊销（知悉时间 ${knownAt}）`,
    );
    return { certId, revokedAt, knownAt, violations };
  }

  /**
   * 证书在指定知悉时间 asOfKnown 下的有效区间（valid time）。
   * 复审延长取最大，吊销截断取最小；返回 null 表示整体失效。
   */
  effectiveValidity(certId: string, asOfKnown: number): Interval | null {
    const cert = this.requireCert(certId);
    const start = cert.validFrom;
    let extendedEnd = cert.validTo;
    let revokedEnd = Infinity;
    for (const event of this.events) {
      if (event.certId !== certId || event.knownAt > asOfKnown) continue;
      if (event.kind === "review") {
        extendedEnd = Math.max(extendedEnd, event.extendedTo);
      } else {
        revokedEnd = Math.min(revokedEnd, event.revokedAt);
      }
    }
    const end = Math.min(extendedEnd, revokedEnd);
    return start < end ? { start, end } : null;
  }

  /** 校验班次在 [start, end) 内是否被员工对应技能资质持续覆盖。 */
  validateShift(shift: Shift, asOfKnown: number): ValidationResult {
    const covering: Interval[] = [];
    for (const cert of this.certs.values()) {
      if (cert.employeeId !== shift.employeeId || cert.skill !== shift.skill) continue;
      const validity = this.effectiveValidity(cert.id, asOfKnown);
      if (validity) covering.push(validity);
    }
    const missing = subtract({ start: shift.start, end: shift.end }, union(covering));
    return { ok: missing.length === 0, missing };
  }

  /** 尝试排班：资质不足则拒绝并返回缺口，不写入排班表。 */
  scheduleShift(shift: Shift, asOfKnown: number): ValidationResult {
    const result = this.validateShift(shift, asOfKnown);
    if (result.ok) {
      this.scheduled.push({ ...shift });
    }
    return result;
  }

  /** 对全部（或满足条件的）已排班次按指定知悉时间重新校验，输出违规列表。 */
  findViolations(
    predicate: (shift: Shift) => boolean = () => true,
    asOfKnown: number,
    reason = "资质复核",
  ): ShiftViolation[] {
    const violations: ShiftViolation[] = [];
    for (const shift of this.scheduled) {
      if (!predicate(shift)) continue;
      const result = this.validateShift(shift, asOfKnown);
      if (!result.ok) {
        violations.push({ shift: { ...shift }, missing: result.missing, reason });
      }
    }
    return violations;
  }

  listScheduled(): Shift[] {
    return this.scheduled.map((shift) => ({ ...shift }));
  }

  private requireCert(certId: string): Certificate {
    const cert = this.certs.get(certId);
    if (!cert) throw new Error(`未知证书: ${certId}`);
    return cert;
  }
}
