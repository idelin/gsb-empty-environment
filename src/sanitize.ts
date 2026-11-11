import type { DataRecord, DataValue } from './ast.ts';
import { BudgetExceededError, TemplateError } from './errors.ts';

/**
 * 保留属性名：即使不可信数据把它们作为自有键携带，也一律丢弃，
 * 保证字段读取永远触碰不到宿主原型链上的任何东西。
 */
export const RESERVED_KEYS: ReadonlySet<string> = new Set([
  '__proto__',
  'constructor',
  'prototype',
]);

export interface SanitizeLimits {
  readonly maxDataNodes: number;
  readonly maxDataDepth: number;
}

function fail(message: string): never {
  throw new TemplateError('INVALID_DATA', message);
}

/**
 * 把不可信输入深拷贝为无原型、冻结的纯数据记录。
 *
 * 拒绝：函数、symbol、bigint、undefined、非有限数字、访问器属性、
 * 类实例（含 Date/Map/Proxy 等一切非普通对象）以及循环引用。
 * 因此净化结果上不存在 getter、不存在原型、不存在宿主对象引用，
 * 解释器后续读取字段时不可能触发任何不可信代码。
 */
export function sanitizeData(input: unknown, limits: SanitizeLimits): DataValue {
  try {
    return visitRoot(input, limits);
  } catch (error) {
    if (error instanceof TemplateError) throw error;
    // Proxy 等恶意对象的陷阱可能抛出任意宿主异常；
    // 统一转换为 INVALID_DATA，不让宿主异常逃逸出引擎。
    throw new TemplateError(
      'INVALID_DATA',
      'untrusted data misbehaved during sanitization',
    );
  }
}

function visitRoot(input: unknown, limits: SanitizeLimits): DataValue {
  let nodes = 0;
  const inStack = new Set<object>();

  const visit = (value: unknown, depth: number): DataValue => {
    if (depth > limits.maxDataDepth) {
      throw new BudgetExceededError(
        'DATA_BUDGET_EXCEEDED',
        `data nesting exceeds ${limits.maxDataDepth}`,
      );
    }
    nodes += 1;
    if (nodes > limits.maxDataNodes) {
      throw new BudgetExceededError(
        'DATA_BUDGET_EXCEEDED',
        `data node count exceeds ${limits.maxDataNodes}`,
      );
    }

    if (value === null) return null;
    switch (typeof value) {
      case 'string':
      case 'boolean':
        return value;
      case 'number':
        if (!Number.isFinite(value)) fail('non-finite number in data');
        return value;
      case 'undefined':
      case 'function':
      case 'symbol':
      case 'bigint':
        fail(`unsupported value of type ${typeof value} in data`);
        break;
      case 'object':
        break;
    }

    const obj = value as object;
    if (inStack.has(obj)) fail('cyclic data structure');

    if (Array.isArray(obj)) {
      inStack.add(obj);
      try {
        const out: DataValue[] = new Array<DataValue>(obj.length);
        for (let i = 0; i < obj.length; i += 1) {
          out[i] = visit(obj[i], depth + 1);
        }
        return Object.freeze(out) as readonly DataValue[];
      } finally {
        inStack.delete(obj);
      }
    }

    const proto: unknown = Object.getPrototypeOf(obj);
    if (proto !== Object.prototype && proto !== null) {
      fail('only plain objects are allowed in data');
    }

    inStack.add(obj);
    try {
      const out: Record<string, DataValue> = Object.create(null) as Record<
        string,
        DataValue
      >;
      for (const key of Object.keys(obj)) {
        if (RESERVED_KEYS.has(key)) continue;
        const desc = Object.getOwnPropertyDescriptor(obj, key);
        if (!desc || !('value' in desc)) {
          fail(`accessor property "${key}" is not allowed in data`);
        }
        out[key] = visit(desc.value, depth + 1);
      }
      return Object.freeze(out) as DataRecord;
    } finally {
      inStack.delete(obj);
    }
  };

  return visit(input, 0);
}

/** 判断净化后的值是否为记录（无原型冻结对象）。 */
export function isDataRecord(value: DataValue): value is DataRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
