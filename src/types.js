/**
 * 小语言类型系统: 对象、字面值与联合。
 *
 * 类型语法(全部有限, 可枚举):
 *   Type  ::= never
 *           | { kind: 'literal', value }                          字面值(字符串/数字/布尔)
 *           | { kind: 'object', fields: {名: {type, optional}} }  对象(可选字段可缺失, 如缺失 tag)
 *           | { kind: 'union', members: [...] }                   联合(已规范化: 拍平/去重/无 never)
 */

/** @typedef {string | number | boolean} LitValue */
/** @typedef {{ kind: 'never' }} NeverType */
/** @typedef {{ kind: 'literal', value: LitValue }} LiteralType */
/** @typedef {{ type: Type, optional: boolean }} Field */
/** @typedef {{ kind: 'object', fields: Record<string, Field> }} ObjectType */
/** @typedef {{ kind: 'union', members: Type[] }} UnionType */
/** @typedef {NeverType | LiteralType | ObjectType | UnionType} Type */

/** @type {NeverType} */
export const never = Object.freeze({ kind: 'never' });

/** @param {LitValue} value @returns {LiteralType} */
export function lit(value) {
  const t = typeof value;
  if (t !== 'string' && t !== 'number' && t !== 'boolean') {
    throw new TypeError(`字面值类型仅支持 string/number/boolean, 收到 ${t}`);
  }
  return { kind: 'literal', value };
}

/** @param {Type} type @returns {Field} */
export const required = (type) => ({ type, optional: false });
/** @param {Type} type @returns {Field} */
export const optional = (type) => ({ type, optional: true });

/** @param {Record<string, Field>} fields @returns {ObjectType} */
export function obj(fields) {
  return { kind: 'object', fields };
}

/**
 * 构造联合并规范化: 拍平嵌套联合、丢弃 never、按结构去重;
 * 空联合归约为 never, 单成员联合归约为该成员。
 * @param {...Type} members @returns {Type}
 */
export function union(...members) {
  const flat = [];
  for (const m of members) {
    if (m.kind === 'never') continue;
    if (m.kind === 'union') flat.push(...m.members);
    else flat.push(m);
  }
  const seen = new Map();
  for (const m of flat) {
    const k = typeKey(m);
    if (!seen.has(k)) seen.set(k, m);
  }
  const uniq = [...seen.values()];
  if (uniq.length === 0) return never;
  if (uniq.length === 1) return uniq[0];
  return { kind: 'union', members: uniq };
}

/** 结构相等判定所需的规范键。 @param {Type} t @returns {string} */
export function typeKey(t) {
  switch (t.kind) {
    case 'never':
      return 'never';
    case 'literal':
      return `lit:${JSON.stringify(t.value)}`;
    case 'object': {
      const parts = Object.keys(t.fields)
        .sort()
        .map((k) => `${k}${t.fields[k].optional ? '?' : ''}:${typeKey(t.fields[k].type)}`);
      return `{${parts.join(',')}}`;
    }
    case 'union':
      return `(${t.members.map(typeKey).sort().join('|')})`;
  }
}

/** @param {Type} a @param {Type} b @returns {boolean} */
export const typeEquals = (a, b) => typeKey(a) === typeKey(b);

/** @param {Type} t @returns {string} */
export function typeToString(t) {
  switch (t.kind) {
    case 'never':
      return 'never';
    case 'literal':
      return JSON.stringify(t.value);
    case 'object': {
      const parts = Object.keys(t.fields).map(
        (k) => `${k}${t.fields[k].optional ? '?' : ''}: ${typeToString(t.fields[k].type)}`,
      );
      return `{ ${parts.join(', ')} }`;
    }
    case 'union':
      return t.members.map(typeToString).join(' | ');
  }
}

const ABSENT = Symbol('absent');

/**
 * 枚举类型 denoted 的全部有限值; 可选字段产生"字段缺失"的取值。
 * @param {Type} t @returns {unknown[]}
 */
export function enumerate(t) {
  switch (t.kind) {
    case 'never':
      return [];
    case 'literal':
      return [t.value];
    case 'union': {
      const out = [];
      const seen = new Set();
      for (const m of t.members) {
        for (const v of enumerate(m)) {
          const k = valueKey(v);
          if (!seen.has(k)) {
            seen.add(k);
            out.push(v);
          }
        }
      }
      return out;
    }
    case 'object': {
      let acc = [{}];
      for (const name of Object.keys(t.fields).sort()) {
        const f = t.fields[name];
        const choices = f.optional ? [ABSENT, ...enumerate(f.type)] : enumerate(f.type);
        const next = [];
        for (const base of acc) {
          for (const c of choices) {
            next.push(c === ABSENT ? base : { ...base, [name]: c });
          }
        }
        acc = next;
      }
      return acc;
    }
  }
}

/** 值的规范键(键序无关), 用于集合比较。 @param {unknown} v @returns {string} */
export function valueKey(v) {
  if (v === null || typeof v !== 'object') return JSON.stringify(v) ?? 'undefined';
  if (Array.isArray(v)) return `[${v.map(valueKey).join(',')}]`;
  const keys = Object.keys(v).sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${valueKey(v[k])}`).join(',')}}`;
}

/** 值是否属于类型(对象允许额外字段, 与 TypeScript 宽度子类型一致)。 */
export function valueInType(v, t) {
  switch (t.kind) {
    case 'never':
      return false;
    case 'literal':
      return v === t.value;
    case 'union':
      return t.members.some((m) => valueInType(v, m));
    case 'object': {
      if (v === null || typeof v !== 'object' || Array.isArray(v)) return false;
      for (const [name, f] of Object.entries(t.fields)) {
        const has = Object.prototype.hasOwnProperty.call(v, name);
        if (!has) {
          if (!f.optional) return false;
          continue;
        }
        if (!valueInType(v[name], f.type)) return false;
      }
      return true;
    }
  }
}

/** 从类型中剔除一个字面值(用于等值判断的否定分支收窄)。 @returns {Type} */
export function removeLiteral(t, value) {
  switch (t.kind) {
    case 'never':
      return never;
    case 'literal':
      return t.value === value ? never : t;
    case 'union':
      return union(...t.members.map((m) => removeLiteral(m, value)));
    case 'object':
      return t;
  }
}

/** 只保留与字面值相等的部分(用于等值判断的肯定分支收窄)。 @returns {Type} */
export function keepOnlyLiteral(t, value) {
  switch (t.kind) {
    case 'never':
      return never;
    case 'literal':
      return t.value === value ? t : never;
    case 'union':
      return union(...t.members.map((m) => keepOnlyLiteral(m, value)));
    case 'object':
      return never;
  }
}
