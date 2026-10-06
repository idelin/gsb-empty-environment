/**
 * 运行侧核对：以有限对象值集合执行同一条件，
 * 验证静态分析给出的剩余类型是否精确（与运行值导出的类型一致）。
 */
import { union, typeEquals, typeToString } from './types.js';
import { typeOfValue } from './values.js';
import { analyzeIf } from './analyzer.js';

/**
 * 在有限值集上执行 `field === lit`。
 * 字段缺失时读取为 undefined，等值不成立（落入 else）。
 * @param {Object<string, *>[]} values
 * @param {string} field
 * @param {string|number|boolean} lit
 * @returns {{ then: Object<string, *>[], else: Object<string, *>[] }}
 */
export function evalCondition(values, field, lit) {
  return {
    then: values.filter((v) => v[field] === lit),
    else: values.filter((v) => v[field] !== lit),
  };
}

/**
 * @typedef {Object} BranchCheck
 * @property {'then'|'else'} branch
 * @property {import('./types.js').Type} staticType  静态剩余类型
 * @property {import('./types.js').Type} runtimeType 运行值导出的精确类型
 * @property {boolean} precise 二者是否结构相等
 */

/**
 * 精度核对：静态收窄结果 vs 有限值集实际执行结果。
 * @param {Object<string, *>[]} values 有限对象值集合
 * @param {string} field
 * @param {string|number|boolean} lit
 * @returns {{ inputType: import('./types.js').Type, checks: BranchCheck[], precise: boolean, report: string }}
 */
export function checkPrecision(values, field, lit) {
  const inputType = union(values.map(typeOfValue));
  const analysis = analyzeIf(inputType, { field, equals: lit });
  const runtime = evalCondition(values, field, lit);

  /** @type {BranchCheck[]} */
  const checks = [
    {
      branch: 'then',
      staticType: analysis.then.type,
      runtimeType: union(runtime.then.map(typeOfValue)),
    },
    {
      branch: 'else',
      staticType: analysis.else.type,
      runtimeType: union(runtime.else.map(typeOfValue)),
    },
  ].map((c) => ({ ...c, precise: typeEquals(c.staticType, c.runtimeType) }));

  const precise = checks.every((c) => c.precise);
  const lines = checks.map(
    (c) =>
      `[${c.branch}] ${c.precise ? '精确' : '不精确'}\n` +
      `  静态剩余类型: ${typeToString(c.staticType)}\n` +
      `  运行导出类型: ${typeToString(c.runtimeType)}`,
  );
  return { inputType, checks, precise, report: lines.join('\n') };
}
