/**
 * 策略模型：只允许显式声明的属性、有限集合成员判断、整数比较与布尔运算。
 * 策略为纯数据 + 本地求值，不接任何外部身份服务。
 */

export type AttributeType = 'string' | 'integer' | 'boolean';

export type AttributeValue = string | number | boolean;

/** 策略加载前必须显式声明允许读取的属性及其类型 */
export interface AttributeSpec {
  name: string;
  type: AttributeType;
}

export type Condition =
  | { kind: 'in'; attribute: string; values: readonly (string | number)[] }
  | { kind: 'int'; attribute: string; op: 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge'; value: number }
  | { kind: 'bool'; attribute: string; value: boolean }
  | { kind: 'and'; all: readonly Condition[] }
  | { kind: 'or'; any: readonly Condition[] }
  | { kind: 'not'; condition: Condition };

export interface PolicyRule {
  id: string;
  effect: 'allow' | 'deny';
  condition: Condition;
  /** 命中该规则的判定结果在缓存中的存活秒数（时间失效点） */
  ttlSeconds: number;
}

export interface Policy {
  /** 规则版本：任何规则变化都必须递增，旧版本产生的允许结果不得沿用 */
  version: number;
  attributes: readonly AttributeSpec[];
  rules: readonly PolicyRule[];
}

function isInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isInteger(value);
}

function validateCondition(
  condition: Condition,
  declared: Map<string, AttributeSpec>,
  path: string,
): void {
  switch (condition.kind) {
    case 'in': {
      const spec = declared.get(condition.attribute);
      if (!spec) throw new Error(`${path}: 属性 "${condition.attribute}" 未在策略中显式声明`);
      if (spec.type === 'boolean') throw new Error(`${path}: 布尔属性 "${condition.attribute}" 不支持集合成员判断`);
      if (!Array.isArray(condition.values) || condition.values.length === 0) {
        throw new Error(`${path}: "in" 条件必须给出非空有限集合`);
      }
      for (const v of condition.values) {
        if (spec.type === 'integer' && !isInteger(v)) {
          throw new Error(`${path}: 集合成员 ${JSON.stringify(v)} 不是整数`);
        }
        if (spec.type === 'string' && typeof v !== 'string') {
          throw new Error(`${path}: 集合成员 ${JSON.stringify(v)} 不是字符串`);
        }
      }
      return;
    }
    case 'int': {
      const spec = declared.get(condition.attribute);
      if (!spec) throw new Error(`${path}: 属性 "${condition.attribute}" 未在策略中显式声明`);
      if (spec.type !== 'integer') throw new Error(`${path}: 属性 "${condition.attribute}" 不是整数类型`);
      if (!isInteger(condition.value)) throw new Error(`${path}: 比较值必须是整数`);
      return;
    }
    case 'bool': {
      const spec = declared.get(condition.attribute);
      if (!spec) throw new Error(`${path}: 属性 "${condition.attribute}" 未在策略中显式声明`);
      if (spec.type !== 'boolean') throw new Error(`${path}: 属性 "${condition.attribute}" 不是布尔类型`);
      if (typeof condition.value !== 'boolean') throw new Error(`${path}: 比较值必须是布尔`);
      return;
    }
    case 'and':
      if (condition.all.length === 0) throw new Error(`${path}: "and" 不能为空`);
      condition.all.forEach((c, i) => validateCondition(c, declared, `${path}.and[${i}]`));
      return;
    case 'or':
      if (condition.any.length === 0) throw new Error(`${path}: "or" 不能为空`);
      condition.any.forEach((c, i) => validateCondition(c, declared, `${path}.or[${i}]`));
      return;
    case 'not':
      validateCondition(condition.condition, declared, `${path}.not`);
      return;
  }
}

/** 校验策略：条件只能引用显式声明的属性，且类型必须匹配 */
export function validatePolicy(policy: Policy): void {
  if (!Number.isInteger(policy.version) || policy.version < 1) {
    throw new Error('策略版本必须是正整数');
  }
  const declared = new Map<string, AttributeSpec>();
  for (const spec of policy.attributes) {
    if (declared.has(spec.name)) throw new Error(`属性 "${spec.name}" 重复声明`);
    declared.set(spec.name, spec);
  }
  policy.rules.forEach((rule, i) => {
    if (!(rule.ttlSeconds > 0)) throw new Error(`规则 "${rule.id}" 的 ttlSeconds 必须为正数`);
    validateCondition(rule.condition, declared, `规则[${i}](${rule.id})`);
  });
}
