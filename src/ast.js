/**
 * 小语言程序 AST: 条件表达式 + 分支决策树。
 *
 * 条件/表达式:
 *   ref            输入值本身
 *   get(field, of) 字段访问(默认作用于 ref)
 *   val(v)         字面值
 *   eq(l, r)       严格相等
 *   and / or / not 布尔组合
 *
 * 程序:
 *   iff(cond, then, else)  条件分支
 *   leaf(label)             叶子(分支结果标记)
 */

export const ref = Object.freeze({ kind: 'ref' });

/** @param {string} field */
export const get = (field, of = ref) => ({ kind: 'get', of, field });
export const val = (value) => ({ kind: 'lit', value });
export const eq = (left, right) => ({ kind: 'eq', left, right });
export const and = (...conds) => ({ kind: 'and', conds });
export const or = (...conds) => ({ kind: 'or', conds });
export const not = (cond) => ({ kind: 'not', cond });

export const iff = (cond, then, otherwise) => ({ kind: 'branch', cond, then, else: otherwise });
export const leaf = (label) => ({ kind: 'leaf', label });

/** @param {object} e @returns {string} */
export function exprToString(e) {
  switch (e.kind) {
    case 'ref':
      return 'value';
    case 'lit':
      return JSON.stringify(e.value);
    case 'get':
      return `${exprToString(e.of)}.${e.field}`;
    case 'eq':
      return `${exprToString(e.left)} === ${exprToString(e.right)}`;
    case 'and':
      return e.conds.map((c) => `(${exprToString(c)})`).join(' && ');
    case 'or':
      return e.conds.map((c) => `(${exprToString(c)})`).join(' || ');
    case 'not':
      return `!(${exprToString(e.cond)})`;
    default:
      return String(e.kind);
  }
}
