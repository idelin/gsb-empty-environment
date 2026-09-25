import type { AttributeValue, Condition, Policy, PolicyRule } from './policy.ts';
import { validatePolicy } from './policy.ts';

/** 求值期间实际读取的属性及其读取时刻的版本 */
export interface AttributeRead {
  attribute: string;
  version: number;
}

export interface AttributeReader {
  readWithVersion(
    subjectId: string,
    attribute: string,
  ): { present: boolean; value: AttributeValue | undefined; version: number };
}

export interface EvaluationOutcome {
  decision: 'allow' | 'deny';
  /** 命中的规则；未命中任何规则时为 null（默认拒绝） */
  matchedRule: PolicyRule | null;
  /** 求值过程中实际读取的属性（含短路求值前已读的部分） */
  reads: readonly AttributeRead[];
}

class TrackedReader {
  readonly reads: AttributeRead[] = [];
  private readonly seen = new Set<string>();
  private readonly inner: AttributeReader;
  private readonly subjectId: string;

  constructor(inner: AttributeReader, subjectId: string) {
    this.inner = inner;
    this.subjectId = subjectId;
  }

  read(attribute: string): { present: boolean; value: AttributeValue | undefined } {
    const result = this.inner.readWithVersion(this.subjectId, attribute);
    if (!this.seen.has(attribute)) {
      this.seen.add(attribute);
      this.reads.push({ attribute, version: result.version });
    }
    return { present: result.present, value: result.value };
  }
}

function evaluateCondition(condition: Condition, reader: TrackedReader): boolean {
  switch (condition.kind) {
    case 'in': {
      const { present, value } = reader.read(condition.attribute);
      if (!present) return false;
      return (condition.values as readonly unknown[]).includes(value);
    }
    case 'int': {
      const { present, value } = reader.read(condition.attribute);
      if (!present) return false;
      const actual = value as number;
      switch (condition.op) {
        case 'eq': return actual === condition.value;
        case 'ne': return actual !== condition.value;
        case 'lt': return actual < condition.value;
        case 'le': return actual <= condition.value;
        case 'gt': return actual > condition.value;
        case 'ge': return actual >= condition.value;
      }
      return false;
    }
    case 'bool': {
      const { present, value } = reader.read(condition.attribute);
      if (!present) return false;
      return value === condition.value;
    }
    case 'and': {
      for (const c of condition.all) {
        if (!evaluateCondition(c, reader)) return false; // 短路：后续属性不读取
      }
      return true;
    }
    case 'or': {
      for (const c of condition.any) {
        if (evaluateCondition(c, reader)) return true;
      }
      return false;
    }
    case 'not':
      return !evaluateCondition(condition.condition, reader);
  }
}

/**
 * 本地纯函数求值：只通过传入的 AttributeReader 读取显式声明的属性，
 * 无任何网络/外部身份服务调用。首个条件成立的规则决定结果，否则默认拒绝。
 */
export function evaluatePolicy(
  policy: Policy,
  subjectId: string,
  reader: AttributeReader,
): EvaluationOutcome {
  validatePolicy(policy);
  const tracked = new TrackedReader(reader, subjectId);
  for (const rule of policy.rules) {
    if (evaluateCondition(rule.condition, tracked)) {
      return { decision: rule.effect, matchedRule: rule, reads: tracked.reads };
    }
  }
  return { decision: 'deny', matchedRule: null, reads: tracked.reads };
}
