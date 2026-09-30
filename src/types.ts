import { canonical, deepEqual, isJSONObject, type JSONValue } from './json.ts';

/**
 * 静态类型模型。
 * - unknown：顶层未知类型，**不是 any**——参与运算只会保守传播并产生警告；
 * - never：底类型，用于标记不可达分支；
 * - 对象字段级 optional 表示“可缺失”。
 */
export type Type =
  | { kind: 'null' }
  | { kind: 'boolean' }
  | { kind: 'number' }
  | { kind: 'string' }
  | { kind: 'literal'; value: JSONValue }
  | { kind: 'array'; items: Type }
  | { kind: 'object'; fields: Record<string, Field> }
  | { kind: 'union'; options: Type[] }
  | { kind: 'unknown' }
  | { kind: 'never' };

export interface Field {
  type: Type;
  optional: boolean;
}

export const tNull: Type = { kind: 'null' };
export const tBoolean: Type = { kind: 'boolean' };
export const tNumber: Type = { kind: 'number' };
export const tString: Type = { kind: 'string' };
export const tUnknown: Type = { kind: 'unknown' };
export const tNever: Type = { kind: 'never' };
export const tLiteral = (value: JSONValue): Type => ({ kind: 'literal', value });
export const tArray = (items: Type): Type => ({ kind: 'array', items });
export const tObject = (fields: Record<string, Field>): Type => ({ kind: 'object', fields });
export const required = (type: Type): Field => ({ type, optional: false });
export const optional = (type: Type): Field => ({ type, optional: true });

/** 规范化类型键：对象键排序（无序），联合选项排序去重，数组保持元素顺序。 */
export function typeKey(t: Type): string {
  switch (t.kind) {
    case 'literal':
      return 'lit(' + canonical(t.value) + ')';
    case 'array':
      return 'array<' + typeKey(t.items) + '>';
    case 'object': {
      const keys = Object.keys(t.fields).sort();
      return (
        '{' +
        keys
          .map((k) => `${k}${t.fields[k]!.optional ? '?' : ''}:${typeKey(t.fields[k]!.type)}`)
          .join(',') +
        '}'
      );
    }
    case 'union':
      return 'union(' + t.options.map(typeKey).sort().join('|') + ')';
    default:
      return t.kind;
  }
}

export function typeEquals(a: Type, b: Type): boolean {
  return typeKey(a) === typeKey(b);
}

/** 构造规范化联合：拍平、去 never、去重、排序；含 unknown 则塌缩为 unknown。 */
export function unionOf(types: Type[]): Type {
  const flat: Type[] = [];
  for (const t of types) {
    if (t.kind === 'never') continue;
    if (t.kind === 'unknown') return tUnknown;
    if (t.kind === 'union') flat.push(...t.options);
    else flat.push(t);
  }
  const seen = new Map<string, Type>();
  for (const t of flat) seen.set(typeKey(t), t);
  const opts = [...seen.values()].sort((a, b) => (typeKey(a) < typeKey(b) ? -1 : 1));
  if (opts.length === 0) return tNever;
  if (opts.length === 1) return opts[0]!;
  return { kind: 'union', options: opts };
}

/** 分支合流：逐字段合并两个对象模式；仅一侧存在的字段标记为可缺失。 */
export function mergeObjectSchemas(a: Type, b: Type): Type {
  if (a.kind === 'never') return b;
  if (b.kind === 'never') return a;
  if (a.kind !== 'object' || b.kind !== 'object') return unionOf([a, b]);
  const fields: Record<string, Field> = {};
  const names = new Set([...Object.keys(a.fields), ...Object.keys(b.fields)]);
  for (const name of names) {
    const fa = a.fields[name];
    const fb = b.fields[name];
    if (fa && fb) {
      fields[name] = { type: unionOf([fa.type, fb.type]), optional: fa.optional || fb.optional };
    } else {
      const f = (fa ?? fb)!;
      fields[name] = { type: f.type, optional: true };
    }
  }
  return { kind: 'object', fields };
}

/** 运行值是否满足静态类型（unknown 匹配一切，never 不匹配任何值）。 */
export function matches(value: JSONValue, t: Type): boolean {
  switch (t.kind) {
    case 'unknown':
      return true;
    case 'never':
      return false;
    case 'null':
      return value === null;
    case 'boolean':
      return typeof value === 'boolean';
    case 'number':
      return typeof value === 'number';
    case 'string':
      return typeof value === 'string';
    case 'literal':
      return deepEqual(value, t.value);
    case 'array':
      return Array.isArray(value) && value.every((v) => matches(v, t.items));
    case 'object': {
      if (!isJSONObject(value)) return false;
      for (const [k, f] of Object.entries(t.fields)) {
        if (k in value) {
          if (!matches(value[k]!, f.type)) return false;
        } else if (!f.optional) {
          return false;
        }
      }
      return true;
    }
    case 'union':
      return t.options.some((o) => matches(value, o));
  }
}

/** 由运行值构造类型（对象字段均为必需）。 */
export function typeOf(value: JSONValue): Type {
  if (value === null) return tNull;
  if (Array.isArray(value)) {
    return tArray(value.length === 0 ? tUnknown : unionOf(value.map(typeOf)));
  }
  switch (typeof value) {
    case 'boolean':
      return tBoolean;
    case 'number':
      return tNumber;
    case 'string':
      return tString;
    case 'object': {
      const fields: Record<string, Field> = {};
      for (const [k, v] of Object.entries(value)) fields[k] = required(typeOf(v));
      return tObject(fields);
    }
  }
}

export function describe(t: Type): string {
  switch (t.kind) {
    case 'literal':
      return JSON.stringify(t.value);
    case 'array':
      return `array<${describe(t.items)}>`;
    case 'object': {
      const keys = Object.keys(t.fields).sort();
      return (
        '{ ' +
        keys.map((k) => `${k}${t.fields[k]!.optional ? '?' : ''}: ${describe(t.fields[k]!.type)}`).join(', ') +
        ' }'
      );
    }
    case 'union':
      return t.options.map(describe).join(' | ');
    default:
      return t.kind;
  }
}

/** 沿路径在静态类型中下钻；穿越联合时保守合并，缺失返回 null。 */
export function walkType(t: Type, segs: (string | number)[]): { type: Type; optional: boolean } | null {
  if (segs.length === 0) return { type: t, optional: false };
  if (t.kind === 'unknown') return { type: tUnknown, optional: false };
  if (t.kind === 'never') return null;
  if (t.kind === 'union') {
    const hits: { type: Type; optional: boolean }[] = [];
    let missing = false;
    for (const o of t.options) {
      const r = walkType(o, segs);
      if (r === null) missing = true;
      else hits.push(r);
    }
    if (hits.length === 0) return null;
    return {
      type: unionOf(hits.map((h) => h.type)),
      optional: missing || hits.some((h) => h.optional),
    };
  }
  const [seg, ...rest] = segs;
  if (typeof seg === 'number') {
    if (t.kind !== 'array') return null;
    return walkType(t.items, rest);
  }
  if (t.kind !== 'object') return null;
  const f = t.fields[seg];
  if (!f) return null;
  const r = walkType(f.type, rest);
  if (r === null) return null;
  return { type: r.type, optional: f.optional || r.optional };
}
