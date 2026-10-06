/**
 * 小语言的类型系统：字面值、（封闭）对象、联合、never。
 * 联合在构造时即规范化：拍平、去重、空联合坍缩为 never、单成员联合坍缩为成员本身。
 *
 * @typedef {{ kind: 'never' }} TNever
 * @typedef {{ kind: 'literal', value: string|number|boolean }} TLiteral
 * @typedef {{ kind: 'object', fields: Object<string, Type> }} TObject
 * @typedef {{ kind: 'union', members: Type[] }} TUnion
 * @typedef {TNever|TLiteral|TObject|TUnion} Type
 */

/** @type {TNever} */
export const NEVER = { kind: 'never' };

/** @param {string|number|boolean} value @returns {TLiteral} */
export const literal = (value) => ({ kind: 'literal', value });

/** @param {Object<string, Type>} fields @returns {TObject} */
export const object = (fields) => ({ kind: 'object', fields });

/**
 * 构造规范化联合。
 * @param {Type[]} members @returns {Type}
 */
export function union(members) {
  const flat = [];
  for (const m of members) {
    if (m.kind === 'never') continue;
    if (m.kind === 'union') flat.push(...m.members);
    else flat.push(m);
  }
  const uniq = [];
  for (const m of flat) {
    if (!uniq.some((u) => typeEquals(u, m))) uniq.push(m);
  }
  if (uniq.length === 0) return NEVER;
  if (uniq.length === 1) return uniq[0];
  return { kind: 'union', members: uniq };
}

/**
 * 结构化类型相等（对象字段顺序无关）。
 * @param {Type} a @param {Type} b @returns {boolean}
 */
export function typeEquals(a, b) {
  if (a.kind !== b.kind) return false;
  switch (a.kind) {
    case 'never':
      return true;
    case 'literal':
      return a.value === /** @type {TLiteral} */ (b).value;
    case 'object': {
      const bf = /** @type {TObject} */ (b).fields;
      const ak = Object.keys(a.fields);
      const bk = Object.keys(bf);
      if (ak.length !== bk.length) return false;
      return ak.every((k) => k in bf && typeEquals(a.fields[k], bf[k]));
    }
    case 'union': {
      const bm = /** @type {TUnion} */ (b).members;
      return (
        a.members.length === bm.length &&
        a.members.every((m) => bm.some((n) => typeEquals(m, n)))
      );
    }
  }
}

/** @param {Type} t @returns {string} */
export function typeToString(t) {
  switch (t.kind) {
    case 'never':
      return 'never';
    case 'literal':
      return JSON.stringify(t.value);
    case 'object': {
      const inner = Object.entries(t.fields)
        .map(([k, v]) => `${k}: ${typeToString(v)}`)
        .join('; ');
      return `{ ${inner} }`;
    }
    case 'union':
      return t.members.map(typeToString).join(' | ');
  }
}
