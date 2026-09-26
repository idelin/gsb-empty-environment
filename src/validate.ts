import type {
  JsonObject,
  PropertySchema,
  RootSchema,
  ValidationError,
} from './types.ts';
import { isPlainObject, joinPtr } from './paths.ts';

// ---- 支持的 schema 子集守门：子集外关键字立即、显式拒绝 ----

const KNOWN_KEYWORDS = new Set([
  'type', 'enum', 'nullable', 'properties', 'required', 'requires', 'compare',
]);
const ALLOWED_OPS = new Set(['<', '<=', '>', '>=']);

function checkSchemaSubset(
  sch: Record<string, unknown>,
  rulePath: string,
  ctx: 'root' | 'property',
): void {
  for (const key of Object.keys(sch)) {
    if (!KNOWN_KEYWORDS.has(key)) {
      throw new Error(
        `未实现的 schema 关键字 ${JSON.stringify(key)}（${rulePath || '/'}）；本实现只支持 type/enum/nullable/properties/required/requires/compare 子集`,
      );
    }
  }
  const type = sch.type;
  if (
    type !== undefined &&
    !['string', 'integer', 'number', 'boolean', 'object'].includes(type as string)
  ) {
    throw new Error(`未实现的类型 ${JSON.stringify(String(type))}（${joinPtr(rulePath, 'type')}）`);
  }
  if (ctx === 'root' && (sch.requires !== undefined || sch.compare !== undefined)) {
    throw new Error(`requires/compare 只能声明在 properties 内的字段上（${rulePath || '/'}）`);
  }
  if (sch.enum !== undefined) {
    if (!Array.isArray(sch.enum) || sch.enum.length === 0) {
      throw new Error(`enum 必须是非空数组（${joinPtr(rulePath, 'enum')}）`);
    }
    for (const ev of sch.enum as unknown[]) {
      if (typeof ev !== 'string' && typeof ev !== 'number') {
        throw new Error(`enum 仅支持字符串/数字成员（${joinPtr(rulePath, 'enum')}）`);
      }
    }
  }
  if (sch.requires !== undefined) {
    if (!Array.isArray(sch.requires) || sch.requires.some((r) => typeof r !== 'string')) {
      throw new Error(`requires 必须是字符串数组（${joinPtr(rulePath, 'requires')}）`);
    }
  }
  if (sch.compare !== undefined) {
    if (!Array.isArray(sch.compare)) {
      throw new Error(`compare 必须是数组（${joinPtr(rulePath, 'compare')}）`);
    }
    (sch.compare as unknown[]).forEach((cr, i) => {
      const rp = joinPtr(joinPtr(rulePath, 'compare'), i);
      if (!isPlainObject(cr)) throw new Error(`compare[${i}] 必须是对象（${rp}）`);
      if (typeof cr.field !== 'string' || !cr.field) {
        throw new Error(`compare.field 必须是非空字符串（${joinPtr(rp, 'field')}）`);
      }
      if (typeof cr.op !== 'string' || !ALLOWED_OPS.has(cr.op)) {
        throw new Error(`compare.op 仅支持 <, <=, >, >=（${joinPtr(rp, 'op')}）`);
      }
    });
  }
  if (isPlainObject(sch.properties)) {
    for (const name of Object.keys(sch.properties)) {
      const sub = sch.properties[name];
      if (!isPlainObject(sub)) {
        throw new Error(`属性 ${JSON.stringify(name)} 的 schema 必须是对象（${joinPtr(joinPtr(rulePath, 'properties'), name)}）`);
      }
      checkSchemaSubset(sub, joinPtr(joinPtr(rulePath, 'properties'), name), 'property');
    }
  }
  if (sch.required !== undefined) {
    if (!Array.isArray(sch.required) || sch.required.some((r) => typeof r !== 'string')) {
      throw new Error(`required 必须是字符串数组（${joinPtr(rulePath, 'required')}）`);
    }
  }
}

export function compileSchema(raw: Record<string, unknown>, ctx: 'root' | 'property' = 'root'): RootSchema {
  checkSchemaSubset(raw, '', ctx);
  const root: RootSchema = {
    type: (raw.type as PropertySchema['type']) ?? 'object',
    ...(raw.enum !== undefined ? { enum: raw.enum as (string | number)[] } : {}),
    ...(raw.nullable !== undefined ? { nullable: raw.nullable as boolean } : {}),
    ...(isPlainObject(raw.properties)
      ? {
          properties: Object.fromEntries(
            Object.entries(raw.properties).map(([k, v]) => [k, compileSchema(v as Record<string, unknown>, 'property')]),
          ),
        }
      : {}),
    ...(Array.isArray(raw.required) ? { required: [...(raw.required as string[])] } : {}),
    ...(Array.isArray(raw.requires) ? { requires: [...(raw.requires as string[])] } : {}),
    ...(Array.isArray(raw.compare)
      ? { compare: (raw.compare as PropertySchema['compare']).map((c) => ({ field: c.field, op: c.op })) }
      : {}),
  };
  return root;
}

// ---- 校验（手写，不借现成验证器） ----

function actualType(v: unknown): string {
  if (v === null) return 'null';
  if (Array.isArray(v)) return 'array';
  return typeof v; // object / string / number / boolean
}

function valueMatchesType(v: unknown, type: PropertySchema['type']): boolean {
  switch (type) {
    case 'string':
      return typeof v === 'string';
    case 'boolean':
      return typeof v === 'boolean';
    case 'integer':
      return typeof v === 'number' && Number.isInteger(v);
    case 'number':
      return typeof v === 'number';
    case 'object':
      return isPlainObject(v);
    default:
      return false;
  }
}

function numericCompare(a: number, b: number, op: string): boolean {
  switch (op) {
    case '<': return a < b;
    case '<=': return a <= b;
    case '>': return a > b;
    case '>=': return a >= b;
    default: return false;
  }
}

export interface ValidationOutput {
  valid: boolean;
  errors: ValidationError[];
}

export function validateInstance(instance: unknown, schema: RootSchema): ValidationOutput {
  const errors: ValidationError[] = [];
  walk(instance, schema, '', '', errors);
  return { valid: errors.length === 0, errors };
}

function walk(
  node: unknown,
  schema: PropertySchema,
  instancePath: string,
  rulePath: string,
  errors: ValidationError[],
): void {
  if (node === null) {
    if (schema.nullable === true) return;
    errors.push({
      kind: 'NULL_NOT_ALLOWED',
      instancePath: instancePath || '/',
      rulePath: joinPtr(rulePath, 'nullable'),
      message: `值为 null，但该节点不允许为空`,
    });
    return;
  }

  const declaredType = schema.type ?? (schema.properties !== undefined ? 'object' : undefined);

  if (declaredType === 'object' || schema.properties !== undefined) {
    if (!isPlainObject(node)) {
      errors.push({
        kind: 'TYPE_MISMATCH',
        instancePath: instancePath || '/',
        rulePath: joinPtr(rulePath, 'type'),
        message: `期望 object，实际为 ${actualType(node)}`,
      });
      return;
    }
    validateObject(node, schema, instancePath, rulePath, errors);
    return;
  }

  if (declaredType !== undefined && !valueMatchesType(node, declaredType)) {
    errors.push({
      kind: 'TYPE_MISMATCH',
      instancePath: instancePath || '/',
      rulePath: joinPtr(rulePath, 'type'),
      message: `期望 ${declaredType}，实际为 ${actualType(node)}（${preview(node)}）`,
    });
    return; // 类型已错，不再做枚举/数值比较
  }
  if (schema.enum !== undefined) {
    const member = schema.enum.find((m) => m === node);
    if (member === undefined) {
      errors.push({
        kind: 'ENUM_MISMATCH',
        instancePath: instancePath || '/',
        rulePath: joinPtr(rulePath, 'enum'),
        message: `值 ${preview(node)} 不在枚举 ${JSON.stringify(schema.enum)} 中`,
      });
    }
  }
}

function validateObject(
  node: Record<string, unknown>,
  schema: PropertySchema,
  instancePath: string,
  rulePath: string,
  errors: ValidationError[],
): void {
  const props = schema.properties ?? {};

  for (const reqName of schema.required ?? []) {
    if (!(reqName in node)) {
      errors.push({
        kind: 'REQUIRED_MISSING',
        instancePath: joinPtr(instancePath, reqName),
        rulePath: joinPtr(rulePath, 'required'),
        message: `缺少必填字段 ${JSON.stringify(reqName)}`,
      });
    }
  }

  for (const [name, childSchema] of Object.entries(props)) {
    const childPath = joinPtr(instancePath, name);
    const childRule = joinPtr(joinPtr(rulePath, 'properties'), name);
    if (name in node) {
      walk(node[name], childSchema, childPath, childRule, errors);
    }
  }

  // 跨字段约束 requires：self 存在 => targets 必须存在
  for (const [name, childSchema] of Object.entries(props)) {
    if (name in node && childSchema.requires) {
      for (const target of childSchema.requires) {
        if (!(target in node)) {
          errors.push({
            kind: 'REQUIRES_MISSING',
            instancePath: joinPtr(instancePath, target),
            rulePath: joinPtr(joinPtr(joinPtr(rulePath, 'properties'), name), 'requires'),
            message: `字段 ${JSON.stringify(name)} 存在时，要求字段 ${JSON.stringify(target)} 也存在`,
          });
        }
      }
    }
  }

  // 跨字段约束 compare
  for (const [name, childSchema] of Object.entries(props)) {
    for (const rule of childSchema.compare ?? []) {
      const selfPath = joinPtr(instancePath, name);
      const otherPath = joinPtr(instancePath, rule.field);
      const rulePathC = joinPtr(joinPtr(joinPtr(rulePath, 'properties'), name), 'compare');
      const selfPresent = name in node;
      const otherPresent = rule.field in node;

      // 比较约束仅在属主字段存在时生效；属主缺失时该约束不适用
      if (!selfPresent) continue;

      if (!otherPresent) {
        errors.push({
          kind: 'COMPARE_FAILED',
          instancePath: selfPath,
          rulePath: rulePathC,
          message: `比较约束 ${JSON.stringify(name)} ${rule.op} ${JSON.stringify(rule.field)} 无法求值：缺少 ${JSON.stringify(rule.field)}`,
          side: 'other',
          otherPath,
          op: rule.op,
        });
        continue;
      }

      const selfVal = node[name];
      const otherVal = node[rule.field];
      const selfProp = props[name];
      const otherProp = props[rule.field];

      let ok: boolean;
      if (typeof selfVal === 'number' && typeof otherVal === 'number') {
        ok = numericCompare(selfVal, otherVal, rule.op);
      } else if (typeof selfVal === 'string' && typeof otherVal === 'string') {
        // 两侧均为同一声明枚举时按枚举顺序比较；否则跨类型不可比
        const bothEnum =
          selfProp?.enum !== undefined &&
          otherProp?.enum !== undefined &&
          JSON.stringify(selfProp.enum) === JSON.stringify(otherProp.enum);
        if (!bothEnum) {
          errors.push({
            kind: 'COMPARE_FAILED',
            instancePath: selfPath,
            rulePath: rulePathC,
            message: `字符串间比较要求两侧声明同一有序枚举：${JSON.stringify(name)} ${rule.op} ${JSON.stringify(rule.field)}`,
            side: 'both',
            otherPath,
            op: rule.op,
          });
          continue;
        }
        const enumMembers = selfProp.enum;
        const i = enumMembers.indexOf(selfVal);
        const j = enumMembers.indexOf(otherVal);
        if (i === -1 || j === -1) {
          // 枚举成员错误由 ENUM_MISMATCH 单独报，这里比较视为暂时失败但不再重复噪音
          continue;
        }
        ok = numericCompare(i, j, rule.op);
      } else {
        errors.push({
          kind: 'COMPARE_FAILED',
          instancePath: selfPath,
          rulePath: rulePathC,
          message: `比较两侧必须同为数字或同为枚举字符串：${JSON.stringify(name)} ${rule.op} ${JSON.stringify(rule.field)}`,
          side: 'both',
          otherPath,
          op: rule.op,
        });
        continue;
      }

      if (!ok) {
        errors.push({
          kind: 'COMPARE_FAILED',
          instancePath: selfPath,
          rulePath: rulePathC,
          message: `比较约束不成立：${preview(selfVal)} ${rule.op} ${preview(otherVal)} 为假`,
          side: 'both',
          otherPath,
          op: rule.op,
        });
      }
    }
  }
}

function preview(v: unknown): string {
  if (typeof v === 'string') return JSON.stringify(v.length > 20 ? v.slice(0, 20) + '…' : v);
  if (v === undefined) return 'undefined';
  return String(v);
}
