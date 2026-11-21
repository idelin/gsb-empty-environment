import {AuditLog, type AuditEntry, type AuditEvent} from './audit.ts';
import {
  PolicyEngine,
  PolicyViolationError,
  type AccessDecision,
  type FieldSchema,
  type Subject,
} from './policy.ts';

export interface ReadOutcome {
  readonly decision: AccessDecision;
  /** 允许时为投影后的记录；拒绝时为 null。 */
  readonly data: Record<string, unknown> | null;
  readonly auditEntry: AuditEntry;
}

export interface ExportOutcome {
  readonly decision: AccessDecision;
  /** 允许时为导出的字段组合；拒绝时为 null。 */
  readonly data: Array<Record<string, unknown>> | null;
  readonly auditEntry: AuditEntry;
}

export type DataRecord = Readonly<Record<string, unknown>>;

/**
 * 分级访问控制门面：每次访问先过策略引擎，再把判定结果写入不可否认留痕。
 * 无论允许还是拒绝都会留痕，且拒绝动作不会返回任何受保护数据。
 */
export class AccessControlService {
  private readonly engine: PolicyEngine;
  private readonly auditLog: AuditLog;

  constructor(schema: FieldSchema, auditLog: AuditLog) {
    this.auditLog = auditLog;
    this.engine = new PolicyEngine(schema);
  }

  private toEvent(
    subject: Subject,
    resource: string,
    decision: AccessDecision,
    requestedFields: readonly string[],
    origin?: string,
  ): AuditEvent {
    return {
      subjectId: subject.id,
      subjectName: subject.name,
      action: decision.action,
      resource,
      requestedFields: [...new Set(requestedFields)],
      allowed: decision.allowed,
      reason: decision.reason,
      violatingFields: decision.violatingFields.map((entry) => entry.field),
      origin,
    };
  }

  /**
   * 读取单条记录的指定字段。
   * 出现任何越级字段即整体拒绝并留痕；合法读取仅返回可见字段投影。
   */
  async readRecord(
    subject: Subject,
    resource: string,
    record: DataRecord,
    requestedFields: readonly string[],
    options: {throwOnDenied?: boolean; origin?: string} = {},
  ): Promise<ReadOutcome> {
    const decision = this.engine.evaluateRead(subject, requestedFields);
    const auditEntry = await this.auditLog.append(
      this.toEvent(subject, resource, decision, requestedFields, options.origin),
    );

    if (!decision.allowed) {
      if (options.throwOnDenied) {
        throw new PolicyViolationError(decision);
      }
      return {decision, data: null, auditEntry};
    }

    const projection: Record<string, unknown> = {};
    for (const field of decision.visibleFields) {
      projection[field] = record[field];
    }
    return {decision, data: projection, auditEntry};
  }

  /**
   * 批量导出多条记录的字段组合。
   * 组合中任一字段超过导出级别即整批拒绝并留痕。
   */
  async exportRecords(
    subject: Subject,
    resource: string,
    records: readonly DataRecord[],
    requestedFields: readonly string[],
    options: {throwOnDenied?: boolean; origin?: string} = {},
  ): Promise<ExportOutcome> {
    const decision = this.engine.evaluateExport(subject, requestedFields);
    const auditEntry = await this.auditLog.append(
      this.toEvent(subject, resource, decision, requestedFields, options.origin),
    );

    if (!decision.allowed) {
      if (options.throwOnDenied) {
        throw new PolicyViolationError(decision);
      }
      return {decision, data: null, auditEntry};
    }

    const data = records.map((record) => {
      const projection: Record<string, unknown> = {};
      for (const field of decision.visibleFields) {
        projection[field] = record[field];
      }
      return projection;
    });
    return {decision, data, auditEntry};
  }
}
