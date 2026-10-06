// @ts-check
/**
 * 主入口：对一组分支（按先后顺序，先匹配者胜出）做穷尽性与冗余性检查。
 */
import { validateSchema, getCtor, computeMinDepth } from './schema.js';
import { WILD, valueDepth } from './pattern.js';
import { useful } from './useful.js';
import { concretize } from './witness.js';

/**
 * @typedef {import('./schema.js').AdtSchema} AdtSchema
 * @typedef {import('./pattern.js').Pattern} Pattern
 * @typedef {import('./pattern.js').Value} Value
 */

/**
 * 校验模式对给定类型是良类型的（构造器属于该类型、元数正确、嵌套递归成立）。
 * @param {AdtSchema} schema
 * @param {string} type
 * @param {Pattern} p
 */
export function validatePattern(schema, type, p) {
  if (p.kind !== 'ctor') return;
  const decl = getCtor(schema, type, p.name);
  if (p.args.length !== decl.args.length) {
    throw new Error(
      `构造器 ${p.name} 需要 ${decl.args.length} 个参数，模式中给了 ${p.args.length} 个`,
    );
  }
  for (let i = 0; i < p.args.length; i++) {
    validatePattern(schema, decl.args[i], p.args[i]);
  }
}

/**
 * @typedef {object} Witness
 * @property {Pattern} pattern  缺失模式（通配符可带 avoid 约束）
 * @property {Value} value      最小构造深度的具体见证值
 * @property {number} depth     见证值的构造深度
 *
 * @typedef {object} CheckResult
 * @property {boolean} exhaustive  是否穷尽（空见证的 vacuous 情形视为穷尽）
 * @property {boolean} vacuous     算法给出缺失模式但类型不可驻留、无具体见证
 * @property {Witness | null} witness  非穷尽时的最小深度见证
 * @property {number[]} redundant  被前面分支完全覆盖（不可达）的分支下标
 */

/**
 * 检查 branches（Pattern 数组，按匹配优先级排列）对类型 type 的匹配。
 * @param {AdtSchema} schema
 * @param {string} type  被匹配值的类型
 * @param {Pattern[]} branches
 * @returns {CheckResult}
 */
export function checkMatch(schema, type, branches) {
  validateSchema(schema);
  if (!(type in schema.types)) throw new Error(`未知类型 ${type}`);
  for (const p of branches) validatePattern(schema, type, p);

  const depths = computeMinDepth(schema);
  const matrix = branches.map((p) => [p]);

  // 穷尽性：通配符相对整个矩阵是否有用
  const witnessVec = useful(schema, depths, matrix, [type], [WILD]);
  /** @type {Witness | null} */
  let witness = null;
  let vacuous = false;
  if (witnessVec) {
    const value = concretize(schema, depths, witnessVec[0], type);
    if (value) {
      witness = { pattern: witnessVec[0], value, depth: valueDepth(value) };
    } else {
      // 缺失模式只经过不可驻留类型：不存在具体的缺失值
      vacuous = true;
    }
  }

  // 冗余性：第 i 行相对前 i-1 行无用 <=> 被完全覆盖（不可达）
  /** @type {number[]} */
  const redundant = [];
  for (let i = 0; i < branches.length; i++) {
    if (useful(schema, depths, matrix.slice(0, i), [type], [branches[i]]) === null) {
      redundant.push(i);
    }
  }

  return { exhaustive: witnessVec === null || vacuous, vacuous, witness, redundant };
}
