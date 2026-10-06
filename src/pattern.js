// @ts-check
/**
 * 模式与值的表示。
 *
 * 模式 Pattern：
 *   { kind: 'wild', avoid? }  通配符 `_`（avoid 为算法内部使用的“须避开的构造器”约束）
 *   { kind: 'var', name }     变量模式（语义等同通配符，仅多一个绑定名）
 *   { kind: 'ctor', name, args }  构造器模式，可任意嵌套
 *
 * 值 Value：{ ctor, args } 的有限树。
 *
 * @typedef {object} WildPat
 * @property {'wild'} kind
 * @property {string[]} [avoid]
 *
 * @typedef {object} VarPat
 * @property {'var'} kind
 * @property {string} name
 *
 * @typedef {object} CtorPat
 * @property {'ctor'} kind
 * @property {string} name
 * @property {Pattern[]} args
 *
 * @typedef {WildPat | VarPat | CtorPat} Pattern
 *
 * @typedef {object} Value
 * @property {string} ctor
 * @property {Value[]} args
 */

/** 共享的通配符单例（只读）。 @type {WildPat} */
export const WILD = Object.freeze({ kind: 'wild' });

/** @returns {WildPat} */
export const wild = () => WILD;

/**
 * 变量模式，如 `x`。
 * @param {string} name
 * @returns {VarPat}
 */
export const v = (name) => ({ kind: 'var', name });

/**
 * 构造器模式，如 `Cons(_, Nil)`。
 * @param {string} name
 * @param {...Pattern} args
 * @returns {CtorPat}
 */
export const ctor = (name, ...args) => ({ kind: 'ctor', name, args });

/**
 * 构造具体值，如 `val('Cons', val('True'), val('Nil'))`。
 * @param {string} name
 * @param {...Value} args
 * @returns {Value}
 */
export const val = (name, ...args) => ({ ctor: name, args });

/**
 * 模式的紧凑打印：`_`、`x`、`Cons(_, Nil)`。
 * @param {Pattern} p
 * @returns {string}
 */
export function prettyPattern(p) {
  switch (p.kind) {
    case 'wild':
      return '_';
    case 'var':
      return p.name;
    case 'ctor':
      return p.args.length === 0
        ? p.name
        : `${p.name}(${p.args.map(prettyPattern).join(', ')})`;
  }
}

/**
 * 值的紧凑打印：`Cons(True, Nil)`。
 * @param {Value} value
 * @returns {string}
 */
export function prettyValue(value) {
  return value.args.length === 0
    ? value.ctor
    : `${value.ctor}(${value.args.map(prettyValue).join(', ')})`;
}

/**
 * 模式是否匹配值（通配符/变量匹配一切）。
 * @param {Pattern} p
 * @param {Value} value
 * @returns {boolean}
 */
export function matchValue(p, value) {
  if (p.kind !== 'ctor') return true;
  return (
    p.name === value.ctor &&
    p.args.length === value.args.length &&
    p.args.every((sub, i) => matchValue(sub, value.args[i]))
  );
}

/**
 * 值的构造深度：叶子为 1。
 * @param {Value} value
 * @returns {number}
 */
export function valueDepth(value) {
  let max = 0;
  for (const a of value.args) max = Math.max(max, valueDepth(a));
  return 1 + max;
}
