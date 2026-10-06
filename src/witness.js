// @ts-check
/**
 * 把算法返回的（含通配符的）见证模式具体化为一个构造深度最小的值。
 * 通配符选择其类型中深度最小的构造器驻留；递归类型因最小深度
 * 严格递减而必然终止，不会无限展开。若某位置要求驻留一个不可驻留
 * 类型（最小深度为 Infinity），返回 null —— 该见证是“空”的。
 */
import { getCtor } from './schema.js';
import { WILD } from './pattern.js';

/**
 * @typedef {import('./schema.js').AdtSchema} AdtSchema
 * @typedef {import('./schema.js').Depths} Depths
 * @typedef {import('./pattern.js').Pattern} Pattern
 * @typedef {import('./pattern.js').Value} Value
 */

/**
 * 模式的最小深度具体化；无法具体化（不可驻留）时返回 null。
 * @param {AdtSchema} schema
 * @param {Depths} depths
 * @param {Pattern} p
 * @param {string} type
 * @returns {Value | null}
 */
export function concretize(schema, depths, p, type) {
  if (p.kind === 'ctor') {
    const decl = getCtor(schema, type, p.name);
    /** @type {Value[]} */
    const args = [];
    for (let i = 0; i < p.args.length; i++) {
      const sub = concretize(schema, depths, p.args[i], decl.args[i]);
      if (!sub) return null;
      args.push(sub);
    }
    return { ctor: p.name, args };
  }
  // 通配符/变量：在允许（未被 avoid 排除）的构造器中选最小深度者
  const avoid = p.kind === 'wild' && p.avoid ? new Set(p.avoid) : null;
  /** @type {import('./schema.js').CtorDecl | null} */
  let best = null;
  let bestDepth = Infinity;
  for (const c of schema.types[type] ?? []) {
    if (avoid && avoid.has(c.name)) continue;
    if (depths.ctorDepth[c.name] < bestDepth) {
      bestDepth = depths.ctorDepth[c.name];
      best = c;
    }
  }
  if (!best || bestDepth === Infinity) return null;
  /** @type {Value[]} */
  const args = [];
  for (const argType of best.args) {
    // best.args 中类型的最小深度严格小于 bestDepth，递归必然终止
    const sub = concretize(schema, depths, WILD, argType);
    if (!sub) return null;
    args.push(sub);
  }
  return { ctor: best.name, args };
}
