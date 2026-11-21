import {isLevel, Level, levelName} from './levels.ts';

/** 字段分级表：字段名 -> 数据级别。 */
export type FieldSchema = Readonly<Record<string, Level>>;

/**
 * 访问主体（用户/服务账号）。
 *
 * - readClearance：可读取的最高级别（在线读取可见范围）。
 * - exportClearance：可批量导出的最高级别，通常不高于 readClearance。
 */
export interface Subject {
  readonly id: string;
  readonly name: string;
  readonly readClearance: Level;
  readonly exportClearance: Level;
}

export type AccessAction = 'READ' | 'EXPORT';

export interface AccessDecision {
  readonly allowed: boolean;
  readonly action: AccessAction;
  readonly subjectId: string;
  /** 本次请求中主体有权看到的字段（按入参顺序去重）。 */
  readonly visibleFields: readonly string[];
  /** 触发拒绝的越级字段及其级别。 */
  readonly violatingFields: ReadonlyArray<{readonly field: string; readonly level: Level}>;
  readonly reason: string;
}

export class PolicyViolationError extends Error {
  readonly decision: AccessDecision;

  constructor(decision: AccessDecision) {
    super(decision.reason);
    this.name = 'PolicyViolationError';
    this.decision = decision;
  }
}

function dedupe(fields: readonly string[]): string[] {
  return [...new Set(fields)];
}

/**
 * 分级访问策略引擎：纯函数式判定，不产生副作用。
 */
export class PolicyEngine {
  private readonly schema: FieldSchema;

  constructor(schema: FieldSchema) {
    this.schema = schema;
  }

 /** 字段是否在主体的读取可见范围内。未知字段一律不可见。 */
  canSeeField(subject: Subject, field: string): boolean {
    const level = this.schema[field];
    return level !== undefined && level <= subject.readClearance;
  }

  /** 返回主体对一条记录实际可见的字段（保持字段表顺序）。 */
  visibleFieldsOf(subject: Subject): string[] {
    return Object.keys(this.schema).filter((field) => this.canSeeField(subject, field));
  }

  /**
   * 判定在线读取请求：只要请求字段中存在任何高于读取级别的字段，
   * 即构成越级读取，整次请求被拒绝。
   */
  evaluateRead(subject: Subject, requestedFields: readonly string[]): AccessDecision {
    const visible: string[] = [];
    const violating: Array<{field: string; level: Level}> = [];

    for (const field of dedupe(requestedFields)) {
      const level = this.schema[field];
      if (level === undefined) {
        violating.push({field, level: Level.SECRET});
        continue;
      }
      if (level <= subject.readClearance) {
        visible.push(field);
      } else {
        violating.push({field, level});
      }
    }

    const allowed = violating.length === 0;
    return {
      allowed,
      action: 'READ',
      subjectId: subject.id,
      visibleFields: allowed ? visible : [],
      reason: allowed
        ? 'READ allowed'
        : `Cross-level READ denied: ${violating
            .map((entry) => `${entry.field}[${levelName(entry.level)}]`)
            .join(', ')} exceeds clearance ${levelName(subject.readClearance)}`,
      violatingFields: violating,
    };
  }

  /**
   * 判定批量导出请求（导出组合）：
   * 导出组合中任一字段高于主体的导出级别即拒绝整批导出，
   * 防止高敏字段借批量导出通道外泄。
   */
  evaluateExport(subject: Subject, requestedFields: readonly string[]): AccessDecision {
    const allowedFields: string[] = [];
    const violating: Array<{field: string; level: Level}> = [];

    for (const field of dedupe(requestedFields)) {
      const level = this.schema[field];
      if (level === undefined) {
        violating.push({field, level: Level.SECRET});
        continue;
      }
      if (level <= subject.exportClearance) {
        allowedFields.push(field);
      } else {
        violating.push({field, level});
      }
    }

    const allowed = violating.length === 0;
    return {
      allowed,
      action: 'EXPORT',
      subjectId: subject.id,
      visibleFields: allowed ? allowedFields : [],
      reason: allowed
        ? 'EXPORT allowed'
        : `Bulk EXPORT denied: combination contains ${violating
            .map((entry) => `${entry.field}[${levelName(entry.level)}]`)
            .join(', ')} beyond export clearance ${levelName(subject.exportClearance)}`,
      violatingFields: violating,
    };
  }
}

export function assertValidLevel(value: number): Level {
  if (!isLevel(value)) {
    throw new TypeError(`Unknown classification level: ${String(value)}`);
  }
  return value;
}
