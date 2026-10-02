/**
 * 内置校验规则库。
 * 每条规则是一个纯对象:{ name, message, validate }。
 * validate 返回 true 表示通过;返回 false 使用规则默认 message;返回字符串则作为当次错误消息。
 */

export interface ValidationContext {
  /** 当前整表值快照(只读语义,校验器不应修改) */
  readonly values: Record<string, unknown>;
  /** 当前被校验字段名 */
  readonly field: string;
}

export type ValidateFn = (value: unknown, ctx: ValidationContext) => boolean | string;

export interface Rule {
  readonly name: string;
  readonly message: string;
  readonly validate: ValidateFn;
}

function makeRule(name: string, message: string, validate: ValidateFn): Rule {
  return { name, message, validate };
}

/** 非空校验:undefined / null / 空字符串 / 空数组 视为缺失 */
export function required(message = '该字段为必填项'): Rule {
  return makeRule('required', message, (value) => {
    if (value === undefined || value === null) return false;
    if (typeof value === 'string') return value.trim().length > 0;
    if (Array.isArray(value)) return value.length > 0;
    return true;
  });
}

export function minLength(min: number, message = `长度不能少于 ${min} 个字符`): Rule {
  return makeRule('minLength', message, (value) => {
    if (value === undefined || value === null || value === '') return true; // 空值交给 required
    return typeof value === 'string' && value.length >= min;
  });
}

export function maxLength(max: number, message = `长度不能超过 ${max} 个字符`): Rule {
  return makeRule('maxLength', message, (value) => {
    if (value === undefined || value === null || value === '') return true;
    return typeof value === 'string' && value.length <= max;
  });
}

export function pattern(regex: RegExp, message = '格式不正确'): Rule {
  return makeRule('pattern', message, (value) => {
    if (value === undefined || value === null || value === '') return true;
    return typeof value === 'string' && regex.test(value);
  });
}

export function email(message = '邮箱格式不正确'): Rule {
  return pattern(/^[^\s@]+@[^\s@]+\.[^\s@]+$/, message);
}

/** 跨字段一致性校验:当前字段值必须等于另一字段的值(如确认密码) */
export function equalsField(otherField: string, message?: string): Rule {
  return makeRule(
    `equalsField:${otherField}`,
    message ?? `必须与字段 ${otherField} 的值保持一致`,
    (value, ctx) => value === ctx.values[otherField],
  );
}

/** 自定义同步校验 */
export function custom(name: string, validate: ValidateFn, message: string): Rule {
  return makeRule(name, message, validate);
}
