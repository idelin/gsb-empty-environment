/**
 * 策略条件模型：只允许三类结构——
 *   1. 有限集合成员判定（in）
 *   2. 整数条件比较（eq/ne/lt/le/gt/ge，操作数必须是整数）
 *   3. 布尔运算（and/or/not）
 * 条件只能引用显式声明的属性（subject.<name> / context.<name>），
 * 不存在任何外部身份服务或隐式数据通道。
 */

export type AttrValue = string | number | boolean;

export type IntOp = 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge';

export type Condition =
  | { readonly op: 'in'; readonly attr: string; readonly values: readonly AttrValue[] }
  | { readonly op: IntOp; readonly attr: string; readonly value: number }
  | { readonly op: 'and'; readonly all: readonly Condition[] }
  | { readonly op: 'or'; readonly any: readonly Condition[] }
  | { readonly op: 'not'; readonly cond: Condition };

/** 读取一个显式属性；属性不存在时返回 undefined。 */
export type ReadFn = (attrRef: string) => AttrValue | undefined;

const ATTR_REF = /^(subject|context)\.[A-Za-z0-9_-]+$/;

export function assertAttrValue(value: AttrValue, where: string): void {
  const t = typeof value;
  if (t === 'string' || t === 'boolean') return;
  if (t === 'number' && Number.isInteger(value)) return;
  throw new TypeError(`${where}: 属性值只允许字符串、布尔或整数，收到 ${String(value)}`);
}

function assertAttrRef(ref: string, where: string): void {
  if (!ATTR_REF.test(ref)) {
    throw new TypeError(`${where}: 非法属性引用 "${ref}"，必须是 subject.<name> 或 context.<name>`);
  }
}

/** 加载策略时校验条件树，拒绝有限集合/整数/布尔运算之外的任何东西。 */
export function validateCondition(cond: Condition, where = 'condition'): void {
  switch (cond.op) {
    case 'in': {
      assertAttrRef(cond.attr, where);
      if (!Array.isArray(cond.values) || cond.values.length === 0) {
        throw new TypeError(`${where}: "in" 需要非空的有限值集合`);
      }
      cond.values.forEach((v, i) => assertAttrValue(v, `${where}.values[${i}]`));
      return;
    }
    case 'eq':
    case 'ne':
    case 'lt':
    case 'le':
    case 'gt':
    case 'ge': {
      assertAttrRef(cond.attr, where);
      if (!Number.isInteger(cond.value)) {
        throw new TypeError(`${where}: "${cond.op}" 的操作数必须是整数，收到 ${String(cond.value)}`);
      }
      return;
    }
    case 'and':
      if (cond.all.length === 0) throw new TypeError(`${where}: "and" 需要非空子条件列表`);
      cond.all.forEach((c, i) => validateCondition(c, `${where}.all[${i}]`));
      return;
    case 'or':
      if (cond.any.length === 0) throw new TypeError(`${where}: "or" 需要非空子条件列表`);
      cond.any.forEach((c, i) => validateCondition(c, `${where}.any[${i}]`));
      return;
    case 'not':
      validateCondition(cond.cond, `${where}.not`);
      return;
  }
}

/**
 * 求值条件。and/or 短路求值：被短路跳过的属性不会被读取，
 * 因此也不会成为缓存条目的失效依赖。
 * 整数条件遇到非整数（含缺失）属性值时一律判定为 false。
 */
export function evaluateCondition(cond: Condition, read: ReadFn): boolean {
  switch (cond.op) {
    case 'in': {
      const actual = read(cond.attr);
      return cond.values.some((v) => v === actual);
    }
    case 'eq':
    case 'ne':
    case 'lt':
    case 'le':
    case 'gt':
    case 'ge': {
      const actual = read(cond.attr);
      if (typeof actual !== 'number' || !Number.isInteger(actual)) return false;
      switch (cond.op) {
        case 'eq': return actual === cond.value;
        case 'ne': return actual !== cond.value;
        case 'lt': return actual < cond.value;
        case 'le': return actual <= cond.value;
        case 'gt': return actual > cond.value;
        case 'ge': return actual >= cond.value;
      }
      return false;
    }
    case 'and':
      for (const c of cond.all) if (!evaluateCondition(c, read)) return false;
      return true;
    case 'or':
      for (const c of cond.any) if (evaluateCondition(c, read)) return true;
      return false;
    case 'not':
      return !evaluateCondition(cond.cond, read);
  }
}
