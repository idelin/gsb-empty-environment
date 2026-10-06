// @ts-check
/**
 * Maranget 的“有用性”（usefulness）算法：判定一个模式向量相对于
 * 模式矩阵是否还能匹配到新的值，并返回一个见证模式向量。
 *
 * - 穷尽性：通配符向量相对整个矩阵有用 <=> 分支不穷尽，见证即缺失模式。
 * - 冗余性：第 i 行相对前 i-1 行无用 <=> 第 i 个分支被前面完全覆盖。
 *
 * 见证的通配符带有 `avoid` 约束（默认矩阵分支须避开已出现的构造器），
 * 并在签名完整时按“最小构造深度”在多个构造器间择优，保证给出的
 * 缺失见证是构造深度最小的。递归类型由 computeMinDepth 的不动点
 * 保证有限，不会无限展开。
 */
import { getCtor, wildMinDepth } from './schema.js';
import { WILD } from './pattern.js';

/**
 * @typedef {import('./schema.js').AdtSchema} AdtSchema
 * @typedef {import('./schema.js').Depths} Depths
 * @typedef {import('./pattern.js').Pattern} Pattern
 */

/**
 * 专门化矩阵 S(c, M)：保留可能与 c(...) 匹配的行，并把行首展开为 c 的各参数。
 * @param {Pattern[][]} matrix
 * @param {string} name  构造器名
 * @param {number} arity 构造器参数个数
 * @returns {Pattern[][]}
 */
export function specialize(matrix, name, arity) {
  /** @type {Pattern[][]} */
  const out = [];
  for (const row of matrix) {
    const [head, ...rest] = row;
    if (head.kind === 'ctor') {
      if (head.name === name) out.push([...head.args, ...rest]);
      // 其它构造器的行被丢弃
    } else {
      const fill = [];
      for (let i = 0; i < arity; i++) fill.push(WILD);
      out.push([...fill, ...rest]);
    }
  }
  return out;
}

/**
 * 默认矩阵 D(M)：保留行首为通配符/变量的行，并去掉行首。
 * @param {Pattern[][]} matrix
 * @returns {Pattern[][]}
 */
export function defaultMatrix(matrix) {
  /** @type {Pattern[][]} */
  const out = [];
  for (const row of matrix) {
    const [head, ...rest] = row;
    if (head.kind !== 'ctor') out.push(rest);
  }
  return out;
}

/**
 * 模式在给定类型下的最小具体化深度（通配符取其类型的最小驻留深度）。
 * @param {AdtSchema} schema
 * @param {Depths} depths
 * @param {string} type
 * @param {Pattern} p
 * @returns {number}
 */
export function patternMinDepth(schema, depths, type, p) {
  if (p.kind === 'ctor') {
    const decl = getCtor(schema, type, p.name);
    let max = 0;
    for (let i = 0; i < p.args.length; i++) {
      max = Math.max(max, patternMinDepth(schema, depths, decl.args[i], p.args[i]));
    }
    return 1 + max;
  }
  return wildMinDepth(schema, depths, type, p.kind === 'wild' ? p.avoid : undefined);
}

/**
 * 见证向量代价 = 各列最小具体化深度的最大值。
 * @param {AdtSchema} schema
 * @param {Depths} depths
 * @param {string[]} colTypes
 * @param {Pattern[]} vec
 * @returns {number}
 */
function vecCost(schema, depths, colTypes, vec) {
  let max = 0;
  for (let i = 0; i < vec.length; i++) {
    max = Math.max(max, patternMinDepth(schema, depths, colTypes[i], vec[i]));
  }
  return max;
}

/**
 * 判定模式向量 vec 相对矩阵 matrix 是否“有用”。
 * 有用则返回一个见证向量（长度与 vec 相同），否则返回 null。
 * 见证在签名完整需要展开通配符时，选择最小构造深度的构造器。
 *
 * @param {AdtSchema} schema
 * @param {Depths} depths
 * @param {Pattern[][]} matrix
 * @param {string[]} colTypes  各列的类型
 * @param {Pattern[]} vec
 * @returns {Pattern[] | null}
 */
export function useful(schema, depths, matrix, colTypes, vec) {
  if (vec.length === 0) {
    // 无列：矩阵为空则空向量有用（存在未被覆盖的值）
    return matrix.length === 0 ? [] : null;
  }
  const [type, ...restTypes] = colTypes;
  const [head, ...restVec] = vec;

  if (head.kind === 'ctor') {
    const decl = getCtor(schema, type, head.name);
    return useful(
      schema,
      depths,
      specialize(matrix, head.name, decl.args.length),
      [...decl.args, ...restTypes],
      [...head.args, ...restVec],
    );
  }

  // head 为通配符/变量
  /** @type {Set<string>} */
  const used = new Set();
  for (const row of matrix) {
    if (row[0].kind === 'ctor') used.add(row[0].name);
  }
  const signature = schema.types[type] ?? [];
  const complete = signature.every((c) => used.has(c.name));

  if (complete) {
    // 签名被行首构造器全覆盖：必须逐构造器展开通配符。
    // 在所有“有用”的构造器中取见证代价最小者 —— 保证最小构造深度。
    /** @type {Pattern[] | null} */
    let best = null;
    let bestCost = Infinity;
    for (const c of signature) {
      const fill = [];
      for (let i = 0; i < c.args.length; i++) fill.push(WILD);
      const sub = useful(
        schema,
        depths,
        specialize(matrix, c.name, c.args.length),
        [...c.args, ...restTypes],
        [...fill, ...restVec],
      );
      if (sub) {
        /** @type {Pattern[]} */
        const w = [
          { kind: 'ctor', name: c.name, args: sub.slice(0, c.args.length) },
          ...sub.slice(c.args.length),
        ];
        const cost = vecCost(schema, depths, colTypes, w);
        if (cost < bestCost) {
          best = w;
          bestCost = cost;
        }
      }
    }
    return best;
  }

  // 签名未覆盖：存在矩阵行首未出现的构造器，走默认矩阵。
  // 见证行首是“避开 used 中构造器”的受约束通配符。
  const sub = useful(schema, depths, defaultMatrix(matrix), restTypes, restVec);
  if (!sub) return null;
  return [{ kind: 'wild', avoid: [...used] }, ...sub];
}
