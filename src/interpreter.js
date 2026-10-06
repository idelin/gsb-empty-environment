/**
 * 动态执行器: 在有限对象值集合上执行条件分支。
 * 字段缺失时访问结果为 undefined, 与任何字面值严格相等比较都为 false。
 */

/** 对单个值求条件/表达式的值。 */
export function evalExpr(value, e) {
  switch (e.kind) {
    case 'ref':
      return value;
    case 'lit':
      return e.value;
    case 'get': {
      const o = evalExpr(value, e.of);
      if (o === null || typeof o !== 'object') return undefined;
      return o[e.field];
    }
    case 'eq':
      return evalExpr(value, e.left) === evalExpr(value, e.right);
    case 'and':
      return e.conds.every((c) => Boolean(evalExpr(value, c)));
    case 'or':
      return e.conds.some((c) => Boolean(evalExpr(value, c)));
    case 'not':
      return !evalExpr(value, e.cond);
    default:
      throw new Error(`未知表达式: ${e.kind}`);
  }
}

/**
 * 在值集合上执行程序。
 * @param {unknown[]} values 有限对象值集合
 * @param {object} program 分支决策树
 * @returns {Map<string, unknown[]>} 叶子标签 → 到达该叶子的值
 */
export function run(values, program) {
  const out = new Map();
  for (const v of values) {
    const label = trace(v, program);
    if (!out.has(label)) out.set(label, []);
    out.get(label).push(v);
  }
  return out;
}

function trace(v, node) {
  if (node.kind === 'leaf') return node.label;
  return evalExpr(v, node.cond) ? trace(v, node.then) : trace(v, node.else);
}
