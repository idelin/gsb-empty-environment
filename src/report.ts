/**
 * 人类可读报告：模型、精确线性系数、假设、精确方差表达式、
 * 标准不确定度（含舍入规则）、不贡献维度与警告。
 */

import { Rational } from './rational.ts';
import type { PropagationResult } from './propagate.ts';

/** 精确值优先用有限十进制表示；否则给分数并附 6 位有效数字近似。 */
export function formatRational(r: Rational): string {
  const exact = r.toDecimalExact();
  if (exact !== null) return exact.toString();
  return `${r.toString()} ≈ ${r.toDecimal(6, 'half-even').toString()}`;
}

export function formatReport(result: PropagationResult): string {
  const lines: string[] = [];
  lines.push('========== 不确定度传播报告 ==========');
  lines.push(`模型: ${result.modelExpr}`);
  lines.push(
    `类型: ${
      result.kind === 'linear-exact'
        ? '线性模型（方差传播为精确恒等式）'
        : '非线性模型（仅一阶泰勒近似）'
    }`,
  );
  lines.push('');
  lines.push('[精确线性系数]');
  for (const c of result.coefficients) lines.push(`  ${c.input}: a = ${c.coeff.toString()}`);
  lines.push('');
  lines.push('[假设]');
  for (const a of result.assumptions) lines.push(`  - ${a}`);
  lines.push('');
  lines.push('[输出估计值]');
  lines.push(`  y = ${result.value.toString()}`);
  lines.push('');
  lines.push('[方差（精确）]');
  lines.push(`  Var(y) = ${result.varianceExpr}`);
  lines.push(`         = ${formatRational(result.variance)}`);
  lines.push('');
  lines.push('[标准不确定度]');
  lines.push(
    `  u(y) = ${result.std.toSigString(result.rounding.sigDigits)}   [√Var 按 ${result.rounding.mode} 舍入到 ${result.rounding.sigDigits} 位有效数字]`,
  );
  lines.push('');
  lines.push('[不贡献维度]');
  if (result.nonContributing.length === 0) {
    lines.push('  （无）');
  } else {
    for (const z of result.nonContributing) lines.push(`  ${z.input}: ${z.reason}`);
  }
  lines.push('');
  lines.push('[警告]');
  if (result.warnings.length === 0) {
    lines.push('  （无）');
  } else {
    for (const w of result.warnings) lines.push(`  - ${w}`);
  }
  return lines.join('\n');
}
