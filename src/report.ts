import { renderTableau, symbolName } from './chase';
import { CheckResult } from './checker';
import { formatAttrSet, formatFd } from './model';

export function renderReport(result: CheckResult): string {
  const { schema, chase, preservation, determinism } = result;
  const lines: string[] = [];

  lines.push('===== 输入 =====');
  lines.push(`R = (${schema.attributes.join(', ')})`);
  lines.push(`F = { ${schema.fds.map((fd) => formatFd(schema, fd)).join(', ')} }`);
  const decompText = schema.decomposition
    .map((member) => formatAttrSet(schema, member))
    .join(', ');
  lines.push(`ρ = { ${decompText} }`);
  lines.push('');

  lines.push('===== [1] 无损连接检验：有限表格追逐 =====');
  lines.push('初始表格:');
  for (const line of renderTableau(schema, chase.symbols, chase.cellSymbol)) {
    lines.push(`  ${line}`);
  }
  lines.push('');
  if (chase.steps.length === 0) {
    lines.push('追逐步骤: 无可应用的合并（无任何 FD 触发符号等同）');
  } else {
    lines.push('追逐步骤（确定性顺序：FD 输入序 → 行对 (i<j) → 右部属性序）:');
    for (const step of chase.steps) {
      const fd = schema.fds[step.fdIndex];
      const lhsText = formatAttrSet(schema, fd.lhs);
      const lhsSymbols = step.lhsSymbols.map((symbol) => symbolName(symbol)).join(', ');
      lines.push(
        `  步骤 ${step.step}: FD#${step.fdIndex + 1} ${formatFd(schema, fd)}；` +
          `行 r${step.rows[0] + 1} 与 r${step.rows[1] + 1} 在 {${lhsText}} 上一致（${lhsSymbols}）；` +
          `合并列 ${schema.attributes[step.column]}：${symbolName(step.absorbed)} 并入 ${symbolName(step.into)}`,
      );
    }
  }
  lines.push('');
  lines.push('最终表格:');
  for (const line of renderTableau(schema, chase.symbols, chase.finalRoots)) {
    lines.push(`  ${line}`);
  }
  lines.push('');
  if (chase.lossless && chase.witnessRow !== null) {
    const stepInfo =
      chase.witnessFirstStep !== null ? `，第 ${chase.witnessFirstStep} 步首次出现` : '，初始表格即存在';
    lines.push(
      `判定: 行 r${chase.witnessRow + 1} 全为 distinguished 符号（a 行）${stepInfo} → 无损连接成立`,
    );
  } else {
    lines.push('判定: 追逐收敛后不存在全 distinguished 符号行 → 无损连接不成立（有损分解）');
  }
  lines.push('');

  lines.push('===== [2] 依赖保持检验 =====');
  for (const entry of preservation.perFd) {
    const fd = schema.fds[entry.fdIndex];
    const trace = entry.closureTrace
      .map((stage) => `{${formatAttrSet(schema, stage)}}`)
      .join(' → ');
    lines.push(`FD#${entry.fdIndex + 1} ${formatFd(schema, fd)}:`);
    lines.push(`  Z 演化: ${trace}`);
    if (entry.preserved) {
      lines.push(`  结论: 保持（右部 {${formatAttrSet(schema, fd.rhs)}} ⊆ Z）`);
    } else {
      lines.push(
        `  结论: 不保持；闭包缺口 = {${formatAttrSet(schema, entry.gap)}}` +
          `（在投影依赖下左部闭包仅达 {${formatAttrSet(schema, entry.reachableClosure)}}，` +
          `无法推出 {${formatAttrSet(schema, entry.gap)}}）`,
      );
    }
  }
  lines.push(`判定: 依赖保持${preservation.preserved ? '成立' : '不成立'}`);
  lines.push('');

  lines.push('===== [3] 总结论 =====');
  lines.push(`无损连接: ${chase.lossless ? '是' : '否'}`);
  lines.push(`依赖保持: ${preservation.preserved ? '是' : '否'}`);
  lines.push('注意: 两项性质相互独立——依赖保持成立不代表无损连接成立，反之亦然；本检查器分别独立判定。');
  lines.push(
    determinism.ok
      ? `可重放性自检: 通过（两次独立追逐产生完全一致的 ${determinism.stepCount} 步合并序列）`
      : '可重放性自检: 失败（两次追逐结果不一致，存在非确定性缺陷）',
  );
  return lines.join('\n');
}
