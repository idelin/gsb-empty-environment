// @ts-check
/**
 * 深度受限的值枚举：递归类型按构造深度截断，绝不无限展开。
 * 用于对算法结果做独立交叉验证，并展示各分支的可达性。
 */
import { matchValue, valueDepth } from './pattern.js';
import { checkMatch } from './check.js';

/**
 * @typedef {import('./schema.js').AdtSchema} AdtSchema
 * @typedef {import('./pattern.js').Pattern} Pattern
 * @typedef {import('./pattern.js').Value} Value
 */

/**
 * 枚举类型 type 中构造深度 <= maxDepth 的全部值（含所有更浅深度的值）。
 * @param {AdtSchema} schema
 * @param {string} type
 * @param {number} maxDepth
 * @returns {Value[]}
 */
export function enumerateValues(schema, type, maxDepth) {
  /** @type {Map<string, Value[]>} */
  const memo = new Map();
  /**
   * @param {string} t
   * @param {number} d
   * @returns {Value[]}
   */
  function gen(t, d) {
    if (d <= 0) return [];
    const key = `${t}:${d}`;
    const hit = memo.get(key);
    if (hit) return hit;
    /** @type {Value[]} */
    const out = [];
    for (const c of schema.types[t] ?? []) {
      if (c.args.length === 0) {
        out.push({ ctor: c.name, args: [] });
        continue;
      }
      /** @type {Value[][]} */
      let combos = [[]];
      for (const argType of c.args) {
        const vs = gen(argType, d - 1);
        /** @type {Value[][]} */
        const next = [];
        for (const combo of combos) {
          for (const v of vs) next.push([...combo, v]);
        }
        combos = next;
      }
      for (const combo of combos) out.push({ ctor: c.name, args: combo });
    }
    memo.set(key, out);
    return out;
  }
  return gen(type, maxDepth);
}

/**
 * @typedef {object} CrossValidation
 * @property {number} maxDepth
 * @property {number} valueCount        枚举到的值总数
 * @property {Value[]} uncovered        深度内未被任何分支覆盖的值
 * @property {number[]} branchHits      各分支“首个命中”的值个数
 * @property {number[]} unreachable     深度内零命中的分支下标
 * @property {string[]} inconsistencies 算法结论与枚举结果不一致之处（应为空）
 * @property {string[]} notes           信息性说明（如深度不足无法确认）
 */

/**
 * 用深度受限枚举交叉验证 checkMatch 的结论：
 * - 算法称穷尽 => 深度内不得存在未覆盖值；
 * - 算法给出深度 d 的见证 => 枚举到的最浅未覆盖值深度必须恰为 d；
 * - 算法称分支冗余 => 该分支在深度内不得有任何首个命中；
 * - 算法称分支可达但深度内零命中 => 记录说明（可能需要更深的值）。
 *
 * @param {AdtSchema} schema
 * @param {string} type
 * @param {Pattern[]} branches
 * @param {number} maxDepth
 * @returns {CrossValidation}
 */
export function crossValidate(schema, type, branches, maxDepth) {
  const result = checkMatch(schema, type, branches);
  const values = enumerateValues(schema, type, maxDepth);

  const branchHits = branches.map(() => 0);
  /** @type {Value[]} */
  const uncovered = [];
  for (const value of values) {
    const idx = branches.findIndex((p) => matchValue(p, value));
    if (idx === -1) uncovered.push(value);
    else branchHits[idx]++;
  }
  const unreachable = [];
  for (let i = 0; i < branches.length; i++) {
    if (branchHits[i] === 0) unreachable.push(i);
  }

  /** @type {string[]} */
  const inconsistencies = [];
  /** @type {string[]} */
  const notes = [];

  if (result.exhaustive && !result.vacuous && uncovered.length > 0) {
    inconsistencies.push(`算法判定穷尽，但枚举发现 ${uncovered.length} 个未覆盖值`);
  }
  if (result.witness) {
    const wd = result.witness.depth;
    if (branches.some((p) => matchValue(p, result.witness.value))) {
      inconsistencies.push('算法给出的见证值竟被某个分支匹配');
    }
    if (wd <= maxDepth) {
      const minUncovered = uncovered.reduce((m, v) => Math.min(m, valueDepth(v)), Infinity);
      if (minUncovered !== wd) {
        inconsistencies.push(
          `见证深度 ${wd} 与枚举发现的最浅未覆盖值深度 ${minUncovered} 不一致`,
        );
      }
    } else {
      notes.push(`见证深度 ${wd} 超出枚举深度 ${maxDepth}，本深度内无法确认`);
    }
  }
  if (result.vacuous) {
    notes.push('缺失模式仅经过不可驻留类型：无具体缺失值（视为穷尽）');
  }
  for (const i of result.redundant) {
    if (branchHits[i] > 0) {
      inconsistencies.push(`算法判定分支 #${i} 冗余，但枚举中它有 ${branchHits[i]} 次命中`);
    }
  }
  for (let i = 0; i < branches.length; i++) {
    if (!result.redundant.includes(i) && branchHits[i] === 0) {
      notes.push(`分支 #${i} 算法判定可达，但深度 ${maxDepth} 内零命中（可能需要更深的值）`);
    }
  }

  return {
    maxDepth,
    valueCount: values.length,
    uncovered,
    branchHits,
    unreachable,
    inconsistencies,
    notes,
  };
}
