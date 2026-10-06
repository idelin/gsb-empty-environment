/**
 * 精度核对: 以有限对象值集合执行条件, 核对静态剩余类型是否精确。
 *
 * 对每个叶子比较两个集合(按值的规范键):
 * - expected: 静态剩余类型枚举出的全部值
 * - actual:   动态执行中实际到达该叶子的值
 *
 * 判定:
 * - exact:      两集合相等 —— 静态剩余类型精确
 * - unsound:    actual 有而 expected 无(missing) —— 静态类型漏掉了真实值
 * - imprecise:  expected 有而 actual 无(spurious) —— 静态类型包含了不会出现的值
 *   (若传入的值集合本就是输入类型的完整枚举, 则 spurious 即真正的静态不精确)
 *
 * 不可达叶子(剩余类型 never)期望动态值集合为空, 否则报告 unsound。
 */

import { enumerate, valueKey, valueInType } from './types.js';
import { analyze } from './analyzer.js';
import { run } from './interpreter.js';

/**
 * @param {import('./types.js').Type} inputType
 * @param {object} program
 * @param {unknown[]} values 有限对象值集合(通常取 enumerate(inputType))
 */
export function checkPrecision(inputType, program, values) {
  const staticResult = analyze(inputType, program);
  const dynamic = run(values, program);
  const outsideInput = values.filter((v) => !valueInType(v, inputType));
  const leaves = staticResult.leaves.map((entry) => {
    const expected = new Set(enumerate(entry.type).map(valueKey));
    const actualValues = dynamic.get(entry.label) ?? [];
    const actual = new Set(actualValues.map(valueKey));
    const missing = [...actual].filter((k) => !expected.has(k));
    const spurious = [...expected].filter((k) => !actual.has(k));
    const status =
      missing.length > 0 && spurious.length > 0
        ? 'unsound+imprecise'
        : missing.length > 0
          ? 'unsound'
          : spurious.length > 0
            ? 'imprecise'
            : 'exact';
    return {
      label: entry.label,
      reachable: entry.reachable,
      residual: entry.type,
      status,
      missing,
      spurious,
    };
  });
  const pass = leaves.every((l) => l.status === 'exact') && outsideInput.length === 0;
  return {
    pass,
    leaves,
    unreachable: staticResult.unreachable,
    diagnostics: staticResult.diagnostics,
    residual: staticResult.residual,
    outsideInput,
  };
}
