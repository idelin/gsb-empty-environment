import { escapePointer, type Schema } from './schema.ts';

/**
 * 独立验证器：手写实现，不依赖 ajv 等现成验证器，也不复用生成器的任何逻辑。
 * 每个错误同时给出实例路径（instancePath，JSON Pointer，'' 表示根）
 * 与规则路径（schemaPath，'# 开头的 schema 内 JSON Pointer）。
 */
export interface ValidationError {
  instancePath: string;
  schemaPath: string;
  keyword: string;
  message: string;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => deepEqual(item, b[i]));
  }
  if (typeof a === 'object') {
    const ka = Object.keys(a as object);
    const kb = Object.keys(b as object);
    if (ka.length !== kb.length) return false;
    return ka.every(
      (k) =>
        Object.prototype.hasOwnProperty.call(b, k) &&
        deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
    );
  }
  return false;
}

function jsonTypeOf(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (typeof value === 'number') return Number.isInteger(value) ? 'integer' : 'number';
  return typeof value;
}

function matchesType(value: unknown, type: string): boolean {
  const actual = jsonTypeOf(value);
  if (type === 'number') return actual === 'number' || actual === 'integer';
  return actual === type;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function validate(instance: unknown, schema: Schema): ValidationError[] {
  const errors: ValidationError[] = [];
  validateNode(instance, schema, '', '#', errors);
  return errors;
}

function validateNode(
  value: unknown,
  schema: Schema,
  instancePath: string,
  schemaPath: string,
  errors: ValidationError[],
): void {
  const fail = (keyword: string, message: string, subInstance = '', subSchema = ''): void => {
    errors.push({
      instancePath: instancePath + subInstance,
      schemaPath: schemaPath + subSchema,
      keyword,
      message,
    });
  };

  if ('const' in schema && !deepEqual(value, schema.const)) {
    fail('const', `值必须恒等于 ${JSON.stringify(schema.const)}`, '', '/const');
  }
  if (schema.enum !== undefined && !schema.enum.some((e) => deepEqual(e, value))) {
    fail('enum', `值不在枚举 ${JSON.stringify(schema.enum)} 内`, '', '/enum');
  }
  if (schema.type !== undefined && !matchesType(value, schema.type)) {
    fail(
      'type',
      `期望类型 ${schema.type}，实际 ${jsonTypeOf(value)}`,
      '',
      '/type',
    );
    return; // 类型不符时其余关键字无意义，避免级联噪音
  }

  if (typeof value === 'string') {
    if (schema.minLength !== undefined && value.length < schema.minLength) {
      fail('minLength', `长度 ${value.length} < minLength ${schema.minLength}`, '', '/minLength');
    }
    if (schema.maxLength !== undefined && value.length > schema.maxLength) {
      fail('maxLength', `长度 ${value.length} > maxLength ${schema.maxLength}`, '', '/maxLength');
    }
    if (schema.pattern !== undefined && !new RegExp(schema.pattern).test(value)) {
      fail('pattern', `不匹配 pattern ${schema.pattern}`, '', '/pattern');
    }
  }

  if (typeof value === 'number') {
    if (schema.minimum !== undefined && value < schema.minimum) {
      fail('minimum', `${value} < minimum ${schema.minimum}`, '', '/minimum');
    }
    if (schema.maximum !== undefined && value > schema.maximum) {
      fail('maximum', `${value} > maximum ${schema.maximum}`, '', '/maximum');
    }
    if (schema.exclusiveMinimum !== undefined && value <= schema.exclusiveMinimum) {
      fail(
        'exclusiveMinimum',
        `${value} <= exclusiveMinimum ${schema.exclusiveMinimum}`,
        '',
        '/exclusiveMinimum',
      );
    }
    if (schema.exclusiveMaximum !== undefined && value >= schema.exclusiveMaximum) {
      fail(
        'exclusiveMaximum',
        `${value} >= exclusiveMaximum ${schema.exclusiveMaximum}`,
        '',
        '/exclusiveMaximum',
      );
    }
    if (schema.multipleOf !== undefined) {
      const q = value / schema.multipleOf;
      if (Math.abs(q - Math.round(q)) > 1e-9) {
        fail('multipleOf', `${value} 不是 ${schema.multipleOf} 的倍数`, '', '/multipleOf');
      }
    }
  }

  if (Array.isArray(value)) {
    if (schema.minItems !== undefined && value.length < schema.minItems) {
      fail('minItems', `元素数 ${value.length} < minItems ${schema.minItems}`, '', '/minItems');
    }
    if (schema.maxItems !== undefined && value.length > schema.maxItems) {
      fail('maxItems', `元素数 ${value.length} > maxItems ${schema.maxItems}`, '', '/maxItems');
    }
    if (schema.uniqueItems) {
      for (let i = 0; i < value.length; i++) {
        for (let j = i + 1; j < value.length; j++) {
          if (deepEqual(value[i], value[j])) {
            fail('uniqueItems', `元素 [${i}] 与 [${j}] 重复`, '', '/uniqueItems');
            i = value.length; // 报告一次即可
            break;
          }
        }
      }
    }
    if (schema.items !== undefined) {
      value.forEach((item, i) =>
        validateNode(item, schema.items!, `${instancePath}/${i}`, `${schemaPath}/items`, errors),
      );
    }
  }

  if (isPlainObject(value)) {
    const props = schema.properties ?? {};
    for (const key of schema.required ?? []) {
      if (!Object.prototype.hasOwnProperty.call(value, key)) {
        fail('required', `缺少必需属性 "${key}"`, '', '/required');
      }
    }
    if (schema.additionalProperties === false) {
      for (const key of Object.keys(value)) {
        if (!Object.prototype.hasOwnProperty.call(props, key)) {
          fail(
            'additionalProperties',
            `存在未声明的额外属性 "${key}"`,
            `/${escapePointer(key)}`,
            '/additionalProperties',
          );
        }
      }
    }
    if (schema.dependentRequired !== undefined) {
      for (const [trigger, deps] of Object.entries(schema.dependentRequired)) {
        if (Object.prototype.hasOwnProperty.call(value, trigger)) {
          for (const dep of deps) {
            if (!Object.prototype.hasOwnProperty.call(value, dep)) {
              fail(
                'dependentRequired',
                `属性 "${trigger}" 存在时要求 "${dep}" 也出现`,
                '',
                '/dependentRequired',
              );
            }
          }
        }
      }
    }
    for (const [key, sub] of Object.entries(props)) {
      if (Object.prototype.hasOwnProperty.call(value, key)) {
        validateNode(
          value[key],
          sub,
          `${instancePath}/${escapePointer(key)}`,
          `${schemaPath}/properties/${escapePointer(key)}`,
          errors,
        );
      }
    }
  }
}
