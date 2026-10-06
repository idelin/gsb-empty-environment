// @ts-check
/**
 * ADT 模式（schema）：有限构造器、每个构造器有若干具类型的参数。
 * 类型之间可以相互递归引用。
 *
 * @typedef {object} CtorDecl
 * @property {string} name            构造器名（全 schema 内唯一）
 * @property {string[]} args          各参数的类型名
 *
 * @typedef {object} AdtSchema
 * @property {Record<string, CtorDecl[]>} types  类型名 -> 构造器列表（有限全集）
 */

/**
 * 校验 schema：引用的类型必须存在；构造器名全局唯一（便于按名字索引深度）。
 * @param {AdtSchema} schema
 */
export function validateSchema(schema) {
  const names = new Set();
  for (const [t, ctors] of Object.entries(schema.types)) {
    if (!Array.isArray(ctors)) throw new Error(`类型 ${t} 的构造器列表必须是数组`);
    for (const c of ctors) {
      if (names.has(c.name)) throw new Error(`构造器名 ${c.name} 被重复定义`);
      names.add(c.name);
      for (const a of c.args) {
        if (!(a in schema.types)) {
          throw new Error(`构造器 ${c.name} 引用了未知类型 ${a}`);
        }
      }
    }
  }
}

/**
 * 取类型 type 的构造器 name 的声明，不存在则抛错。
 * @param {AdtSchema} schema
 * @param {string} type
 * @param {string} name
 * @returns {CtorDecl}
 */
export function getCtor(schema, type, name) {
  const decl = (schema.types[type] ?? []).find((c) => c.name === name);
  if (!decl) throw new Error(`${name} 不是类型 ${type} 的构造器`);
  return decl;
}

/**
 * @typedef {object} Depths
 * @property {Record<string, number>} typeDepth  类型的最小驻留深度（不可驻留为 Infinity）
 * @property {Record<string, number>} ctorDepth  构造器的最小驻留深度
 */

/**
 * 用不动点迭代计算每个类型/构造器的“最小构造深度”。
 * 递归类型不会无限展开：深度只减不增且以 1 为下界，最多迭代到收敛。
 * 深度定义：叶子构造器为 1，其余为 1 + max(各参数类型的最小深度)。
 * 若某类型不存在有限值（如 `Loop = Wrap(Loop)`），其深度保持 Infinity。
 *
 * @param {AdtSchema} schema
 * @returns {Depths}
 */
export function computeMinDepth(schema) {
  /** @type {Record<string, number>} */
  const typeDepth = {};
  /** @type {Record<string, number>} */
  const ctorDepth = {};
  for (const t of Object.keys(schema.types)) typeDepth[t] = Infinity;
  for (const ctors of Object.values(schema.types)) {
    for (const c of ctors) ctorDepth[c.name] = Infinity;
  }
  let changed = true;
  while (changed) {
    changed = false;
    for (const [t, ctors] of Object.entries(schema.types)) {
      for (const c of ctors) {
        let maxArg = 0;
        for (const a of c.args) maxArg = Math.max(maxArg, typeDepth[a]);
        const d = 1 + maxArg; // Infinity 传播：1 + Infinity = Infinity
        if (d < ctorDepth[c.name]) {
          ctorDepth[c.name] = d;
          changed = true;
        }
        if (d < typeDepth[t]) {
          typeDepth[t] = d;
          changed = true;
        }
      }
    }
  }
  return { typeDepth, ctorDepth };
}

/**
 * 某类型在“避开 avoid 中的构造器”时的最小驻留深度。
 * 用于默认矩阵分支产生的带约束通配符见证。
 *
 * @param {AdtSchema} schema
 * @param {Depths} depths
 * @param {string} type
 * @param {string[] | undefined} [avoid]
 * @returns {number}
 */
export function wildMinDepth(schema, depths, type, avoid) {
  let best = Infinity;
  const banned = avoid && avoid.length > 0 ? new Set(avoid) : null;
  for (const c of schema.types[type] ?? []) {
    if (banned && banned.has(c.name)) continue;
    if (depths.ctorDepth[c.name] < best) best = depths.ctorDepth[c.name];
  }
  return best;
}
