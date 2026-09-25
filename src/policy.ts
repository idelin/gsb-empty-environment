/**
 * 策略引擎：只读调用方显式传入的属性，不访问任何外部身份服务。
 * 条件被限制为三种形式：
 *   1. 有限集合成员（in）
 *   2. 整数比较（eq/neq/lt/lte/gt/gte，两侧都必须是整数）
 *   3. 布尔运算（and/or/not）
 */
export type AttributeValue = string | number | boolean;
export type Attributes = Readonly<Record<string, AttributeValue>>;

export type Condition =
  | { readonly op: 'in'; readonly attr: string; readonly set: readonly (string | number)[] }
  | { readonly op: 'eq' | 'neq' | 'lt' | 'lte' | 'gt' | 'gte'; readonly attr: string; readonly value: number }
  | { readonly op: 'and'; readonly all: readonly Condition[] }
  | { readonly op: 'or'; readonly any: readonly Condition[] }
  | { readonly op: 'not'; readonly cond: Condition };

export interface Policy {
  readonly name: string;
  /** 策略允许读取的显式属性白名单；评估时只访问这些键。 */
  readonly requiredAttributes: readonly string[];
  readonly condition: Condition;
}

export type PolicyDecision =
  | { readonly decision: 'allow' }
  | { readonly decision: 'deny'; readonly reasons: readonly string[] };

function collectAttrs(cond: Condition, into: Set<string>): void {
  switch (cond.op) {
    case 'in':
    case 'eq':
    case 'neq':
    case 'lt':
    case 'lte':
    case 'gt':
    case 'gte':
      into.add(cond.attr);
      return;
    case 'and':
      for (const c of cond.all) collectAttrs(c, into);
      return;
    case 'or':
      for (const c of cond.any) collectAttrs(c, into);
      return;
    case 'not':
      collectAttrs(cond.cond, into);
      return;
  }
}

function evalCondition(cond: Condition, attrs: Attributes, reasons: string[]): boolean {
  switch (cond.op) {
    case 'in': {
      if (!Array.isArray(cond.set)) {
        reasons.push(`attr "${cond.attr}": set must be a finite array`);
        return false;
      }
      const value = attrs[cond.attr];
      if (value === undefined) {
        reasons.push(`missing attribute "${cond.attr}"`);
        return false;
      }
      return cond.set.includes(value as string | number);
    }
    case 'eq':
    case 'neq':
    case 'lt':
    case 'lte':
    case 'gt':
    case 'gte': {
      if (!Number.isInteger(cond.value)) {
        reasons.push(`condition on "${cond.attr}": comparison value must be an integer`);
        return false;
      }
      const value = attrs[cond.attr];
      if (value === undefined) {
        reasons.push(`missing attribute "${cond.attr}"`);
        return false;
      }
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        reasons.push(`attr "${cond.attr}": integer condition requires an integer attribute`);
        return false;
      }
      switch (cond.op) {
        case 'eq': return value === cond.value;
        case 'neq': return value !== cond.value;
        case 'lt': return value < cond.value;
        case 'lte': return value <= cond.value;
        case 'gt': return value > cond.value;
        case 'gte': return value >= cond.value;
      }
      return false;
    }
    case 'and':
      return cond.all.every((c) => evalCondition(c, attrs, reasons));
    case 'or':
      return cond.any.some((c) => evalCondition(c, attrs, reasons));
    case 'not':
      return !evalCondition(cond.cond, attrs, reasons);
  }
}

export function evaluatePolicy(policy: Policy, attrs: Attributes): PolicyDecision {
  const used = new Set<string>();
  collectAttrs(policy.condition, used);
  const allowed = new Set(policy.requiredAttributes);
  const reasons: string[] = [];
  for (const attr of used) {
    if (!allowed.has(attr)) {
      reasons.push(`attribute "${attr}" is not in the policy's explicit attribute list`);
    }
  }
  if (reasons.length > 0) return { decision: 'deny', reasons };
  // 只读取白名单内的显式属性，构造受限视图，杜绝读取其他来源。
  const view: Record<string, AttributeValue> = {};
  for (const attr of used) {
    if (Object.prototype.hasOwnProperty.call(attrs, attr)) view[attr] = attrs[attr];
  }
  const ok = evalCondition(policy.condition, view, reasons);
  return ok ? { decision: 'allow' } : { decision: 'deny', reasons };
}
