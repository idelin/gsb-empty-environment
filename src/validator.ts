/**
 * 预算受限的 JSON Schema 子集验证器（不依赖任何现成验证器）。
 *
 * 设计要点：
 * - 解析（schema 遍历）、$ref 展开、字符串匹配、错误收集共享同一个预算计数器；
 *   预算耗尽时返回 status: 'incomplete'，而不是 'invalid'。
 * - 支持 AbortSignal 取消；取消或耗尽后清理全部临时状态（引用缓存、递归深度等）。
 * - 每个 $ref 出现都单独计费（即使目标已缓存），大量重复引用无法绕过预算。
 * - maxErrors 达到上限时返回 'incomplete'（reason: 'errorLimitReached'），
 *   与输入确实无效的 'invalid' 明确区分。
 * - 所有状态均为每次调用新建，下次调用不会继承上次的耗尽状态。
 */

export type ValidationStatus = 'valid' | 'invalid' | 'incomplete';

export type IncompleteReason =
  | 'budgetExhausted'
  | 'cancelled'
  | 'errorLimitReached';

export interface ValidationError {
  /** 实例中的位置（JSON Pointer，如 /items/0/name） */
  instancePath: string;
  /** 命中规则在 schema 中的位置（JSON Pointer，如 /properties/name/minLength） */
  schemaPath: string;
  message: string;
}

export interface ValidationResult {
  status: ValidationStatus;
  errors: ValidationError[];
  /** 仅当 status 为 'incomplete' 时存在 */
  incompleteReason?: IncompleteReason;
  budget: { limit: number; consumed: number };
}

export interface ValidateOptions {
  /** 共享预算上限，默认 10000 */
  budget?: number;
  /** 错误收集上限，默认 100；达到后返回 incomplete/errorLimitReached */
  maxErrors?: number;
  /** 取消信号 */
  signal?: AbortSignal;
}

/** schema 使用了承诺子集之外的功能时抛出（调用方错误，非验证结论） */
export class SchemaError extends Error {}

class BudgetExhausted extends Error {}
class Cancelled extends Error {}
class ErrorLimitReached extends Error {}

const DEFAULT_BUDGET = 10_000;
const DEFAULT_MAX_ERRORS = 100;
/** 递归深度硬上限，防止循环 $ref 在预算耗尽前撑爆调用栈 */
const MAX_DEPTH = 500;
/** 每遇到一个 $ref 的固定解析成本（无论是否命中缓存） */
const REF_RESOLUTION_COST = 10;

interface Context {
  rootSchema: unknown;
  budgetLimit: number;
  consumed: number;
  maxErrors: number;
  signal: AbortSignal | undefined;
  errors: ValidationError[];
  /** 仅缓存 JSON Pointer 的查找结果；每次 $ref 仍按次计费 */
  refCache: Map<string, unknown>;
  depth: number;
}

/** 所有工作都通过这里计费：解析、引用展开、字符串匹配、错误收集共享同一预算 */
function charge(ctx: Context, cost: number): void {
  if (ctx.signal?.aborted) throw new Cancelled();
  ctx.consumed += cost;
  if (ctx.consumed > ctx.budgetLimit) throw new BudgetExhausted();
}

function addError(
  ctx: Context,
  instancePath: string,
  schemaPath: string,
  message: string,
): void {
  charge(ctx, 1); // 错误收集同样消耗预算
  ctx.errors.push({ instancePath, schemaPath, message });
  if (ctx.errors.length >= ctx.maxErrors) throw new ErrorLimitReached();
}

function escapePointer(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

function unescapePointer(segment: string): string {
  return segment.replace(/~1/g, '/').replace(/~0/g, '~');
}

/** 仅支持本地 JSON Pointer 形式的 $ref（"#/..." 或 "#"） */
function resolveRef(ctx: Context, ref: string): unknown {
  if (ref !== '#' && !ref.startsWith('#/')) {
    throw new SchemaError(`仅支持本地 $ref（"#/..."），收到: ${ref}`);
  }
  const cached = ctx.refCache.get(ref);
  if (cached !== undefined) return cached;
  const segments =
    ref === '#' ? [] : ref.slice(2).split('/').map(unescapePointer);
  let node: unknown = ctx.rootSchema;
  for (const segment of segments) {
    charge(ctx, 1); // 指针逐段行走计入共享预算
    if (typeof node !== 'object' || node === null || Array.isArray(node)) {
      throw new SchemaError(`无法解析的 $ref: ${ref}`);
    }
    const record = node as Record<string, unknown>;
    if (!(segment in record)) {
      throw new SchemaError(`无法解析的 $ref: ${ref}`);
    }
    node = record[segment];
  }
  ctx.refCache.set(ref, node);
  return node;
}

function jsonType(value: unknown): string {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (typeof value === 'number') {
    return Number.isInteger(value) ? 'integer' : 'number';
  }
  return typeof value; // string | boolean | object
}

function typeMatches(actual: string, expected: string): boolean {
  if (expected === 'number') return actual === 'number' || actual === 'integer';
  return actual === expected;
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) {
    return false;
  }
  const aArr = Array.isArray(a);
  if (aArr !== Array.isArray(b)) return false;
  if (aArr) {
    const bb = b as unknown[];
    if (a.length !== bb.length) return false;
    return a.every((v, i) => deepEqual(v, bb[i]));
  }
  const ra = a as Record<string, unknown>;
  const rb = b as Record<string, unknown>;
  const ka = Object.keys(ra);
  if (ka.length !== Object.keys(rb).length) return false;
  return ka.every((k) => k in rb && deepEqual(ra[k], rb[k]));
}

function validateNode(
  ctx: Context,
  schema: unknown,
  instance: unknown,
  instancePath: string,
  schemaPath: string,
): void {
  charge(ctx, 1); // schema 节点遍历（"解析"）计入共享预算
  ctx.depth += 1;
  if (ctx.depth > MAX_DEPTH) throw new BudgetExhausted();
  try {
    if (typeof schema === 'boolean') {
      if (!schema) {
        addError(ctx, instancePath, schemaPath, '布尔 schema 为 false，拒绝一切实例');
      }
      return;
    }
    if (typeof schema !== 'object' || schema === null || Array.isArray(schema)) {
      throw new SchemaError('schema 必须是对象或布尔值');
    }
    const s = schema as Record<string, unknown>;

    // ---- $ref：每次出现都计费，重复引用无法绕过预算 ----
    if (typeof s.$ref === 'string') {
      charge(ctx, REF_RESOLUTION_COST);
      const target = resolveRef(ctx, s.$ref);
      validateNode(ctx, target, instance, instancePath, `${schemaPath}/$ref`);
      return;
    }

    // ---- type ----
    let actual = jsonType(instance);
    if (s.type !== undefined) {
      const expected = Array.isArray(s.type) ? s.type : [s.type];
      if (!expected.every((t) => typeof t === 'string')) {
        throw new SchemaError('type 必须是字符串或字符串数组');
      }
      if (!expected.some((t) => typeMatches(actual, t as string))) {
        addError(
          ctx,
          instancePath,
          `${schemaPath}/type`,
          `期望类型 ${expected.join('|')}，实际为 ${actual}`,
        );
        return; // 类型不符时不再套用同级的类型相关关键字
      }
    }

    // ---- enum / const ----
    if (Array.isArray(s.enum)) {
      let matched = false;
      for (const candidate of s.enum) {
        charge(ctx, 1);
        if (deepEqual(candidate, instance)) {
          matched = true;
          break;
        }
      }
      if (!matched) {
        addError(ctx, instancePath, `${schemaPath}/enum`, '值不在 enum 允许范围内');
      }
    }
    if ('const' in s) {
      charge(ctx, 1);
      if (!deepEqual(s.const, instance)) {
        addError(ctx, instancePath, `${schemaPath}/const`, '值不等于 const');
      }
    }

    // ---- 字符串 ----
    if (typeof instance === 'string') {
      const length = Array.from(instance).length;
      if (typeof s.minLength === 'number') {
        charge(ctx, 1);
        if (length < s.minLength) {
          addError(ctx, instancePath, `${schemaPath}/minLength`,
            `长度 ${length} 小于 minLength ${s.minLength}`);
        }
      }
      if (typeof s.maxLength === 'number') {
        charge(ctx, 1);
        if (length > s.maxLength) {
          addError(ctx, instancePath, `${schemaPath}/maxLength`,
            `长度 ${length} 超过 maxLength ${s.maxLength}`);
        }
      }
      if (typeof s.pattern === 'string') {
        // 字符串匹配按输入长度计费，与解析/引用共享同一预算
        charge(ctx, Math.max(1, length));
        let regex: RegExp;
        try {
          regex = new RegExp(s.pattern);
        } catch {
          throw new SchemaError(`非法的 pattern 正则: ${s.pattern}`);
        }
        if (!regex.test(instance)) {
          addError(ctx, instancePath, `${schemaPath}/pattern`,
            `不匹配 pattern ${s.pattern}`);
        }
      }
    }

    // ---- 数值 ----
    if (typeof instance === 'number') {
      const checks: Array<[string, (v: number, l: number) => boolean, string]> = [
        ['minimum', (v, l) => v >= l, '小于'],
        ['maximum', (v, l) => v <= l, '大于'],
        ['exclusiveMinimum', (v, l) => v > l, '不大于'],
        ['exclusiveMaximum', (v, l) => v < l, '不小于'],
      ];
      for (const [keyword, ok, desc] of checks) {
        const limit = s[keyword];
        if (limit !== undefined) {
          if (typeof limit !== 'number') {
            throw new SchemaError(`${keyword} 必须是数字`);
          }
          charge(ctx, 1);
          if (!ok(instance, limit)) {
            addError(ctx, instancePath, `${schemaPath}/${keyword}`,
              `${instance} ${desc} ${keyword} ${limit}`);
          }
        }
      }
    }

    // ---- 对象 ----
    if (actual === 'object') {
      const obj = instance as Record<string, unknown>;
      if (Array.isArray(s.required)) {
        for (const key of s.required) {
          charge(ctx, 1);
          if (typeof key !== 'string') throw new SchemaError('required 必须是字符串数组');
          if (!(key in obj)) {
            addError(ctx, instancePath, `${schemaPath}/required`,
              `缺少必需属性 ${key}`);
          }
        }
      }
      if (s.properties !== undefined) {
        if (typeof s.properties !== 'object' || s.properties === null) {
          throw new SchemaError('properties 必须是对象');
        }
        for (const [key, subSchema] of Object.entries(
          s.properties as Record<string, unknown>,
        )) {
          if (key in obj) {
            validateNode(
              ctx,
              subSchema,
              obj[key],
              `${instancePath}/${escapePointer(key)}`,
              `${schemaPath}/properties/${escapePointer(key)}`,
            );
          }
        }
      }
      if (s.additionalProperties !== undefined) {
        if (typeof s.additionalProperties !== 'boolean') {
          throw new SchemaError('additionalProperties 仅支持布尔形式');
        }
        if (s.additionalProperties === false) {
          const declared = new Set(
            typeof s.properties === 'object' && s.properties !== null
              ? Object.keys(s.properties)
              : [],
          );
          for (const key of Object.keys(obj)) {
            charge(ctx, 1);
            if (!declared.has(key)) {
              addError(ctx, `${instancePath}/${escapePointer(key)}`,
                `${schemaPath}/additionalProperties`,
                `存在未声明的额外属性 ${key}`);
            }
          }
        }
      }
    }

    // ---- 数组 ----
    if (actual === 'array') {
      const arr = instance as unknown[];
      if (typeof s.minItems === 'number') {
        charge(ctx, 1);
        if (arr.length < s.minItems) {
          addError(ctx, instancePath, `${schemaPath}/minItems`,
            `数组长度 ${arr.length} 小于 minItems ${s.minItems}`);
        }
      }
      if (typeof s.maxItems === 'number') {
        charge(ctx, 1);
        if (arr.length > s.maxItems) {
          addError(ctx, instancePath, `${schemaPath}/maxItems`,
            `数组长度 ${arr.length} 超过 maxItems ${s.maxItems}`);
        }
      }
      if (s.items !== undefined) {
        if (Array.isArray(s.items)) {
          throw new SchemaError('items 仅支持单 schema 形式（不支持元组形式）');
        }
        for (let i = 0; i < arr.length; i += 1) {
          validateNode(
            ctx,
            s.items,
            arr[i],
            `${instancePath}/${i}`,
            `${schemaPath}/items`,
          );
        }
      }
    }
  } finally {
    ctx.depth -= 1;
  }
}

/** 清理本次调用产生的全部临时状态 */
function cleanup(ctx: Context): void {
  ctx.refCache.clear();
  ctx.errors.length = 0;
  ctx.rootSchema = undefined;
  ctx.signal = undefined;
  ctx.depth = 0;
}

function makeResult(
  ctx: Context,
  status: ValidationStatus,
  incompleteReason?: IncompleteReason,
): ValidationResult {
  return {
    status,
    errors: ctx.errors.slice(),
    ...(incompleteReason !== undefined ? { incompleteReason } : {}),
    budget: { limit: ctx.budgetLimit, consumed: ctx.consumed },
  };
}

export function validate(
  schema: unknown,
  instance: unknown,
  options: ValidateOptions = {},
): ValidationResult {
  // 每次调用都新建上下文：预算、缓存、错误列表互不继承
  const ctx: Context = {
    rootSchema: schema,
    budgetLimit: options.budget ?? DEFAULT_BUDGET,
    consumed: 0,
    maxErrors: options.maxErrors ?? DEFAULT_MAX_ERRORS,
    signal: options.signal,
    errors: [],
    refCache: new Map(),
    depth: 0,
  };
  let result: ValidationResult;
  try {
    validateNode(ctx, schema, instance, '', '');
    result = makeResult(ctx, ctx.errors.length > 0 ? 'invalid' : 'valid');
  } catch (err) {
    if (err instanceof BudgetExhausted) {
      result = makeResult(ctx, 'incomplete', 'budgetExhausted');
    } else if (err instanceof Cancelled) {
      result = makeResult(ctx, 'incomplete', 'cancelled');
    } else if (err instanceof ErrorLimitReached) {
      result = makeResult(ctx, 'incomplete', 'errorLimitReached');
    } else {
      throw err; // SchemaError 等调用方错误继续抛出
    }
  } finally {
    cleanup(ctx);
  }
  return result;
}
