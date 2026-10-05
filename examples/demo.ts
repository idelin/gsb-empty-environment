// 演示：有序规则的覆盖分析 + 增删规则后的更新。
// 运行：node examples/demo.ts   （需要 Node >= 23.6，或 22.6+ 加 --experimental-strip-types）

import { RuleCoverageAnalyzer } from '../src/analyzer.ts';
import { formatBox, formatInput, indexSchema } from '../src/box.ts';
import { enumEq, intRange } from '../src/model.ts';
import type { AnalysisReport } from '../src/analyzer.ts';
import type { Schema } from '../src/model.ts';

const schema: Schema = {
  attrs: [
    { kind: 'int', name: 'age', min: 0, max: 120 },
    { kind: 'enum', name: 'tier', values: ['free', 'pro', 'enterprise'] },
  ],
};

const analyzer = new RuleCoverageAnalyzer(schema, [
  { id: 'block-kids', action: 'deny', conditions: [intRange('age', 0, 13)] },
  { id: 'teen-free', action: 'trial', conditions: [intRange('age', 10, 18), enumEq('tier', 'free')] },
  { id: 'ent-all', action: 'allow', conditions: [enumEq('tier', 'enterprise')] },
  { id: 'dead-empty', action: 'allow', conditions: [intRange('age', 30, 30)] },
  { id: 'adult-pro', action: 'allow', conditions: [intRange('age', 18, 65), enumEq('tier', 'pro')] },
]);

function printReport(title: string, report: AnalysisReport): void {
  const idx = indexSchema(schema);
  console.log(`\n=== ${title} ===`);
  for (const r of report.rules) {
    const witness = r.witness ? ` 最小反例=${formatInput(idx, r.witness)}` : '';
    const shadow = r.shadowedBy.length > 0 ? ` 遮蔽来源=[${r.shadowedBy.join(', ')}]` : '';
    console.log(`  ${r.ruleId} (${r.action}): ${r.status}${witness}${shadow}`);
  }
  if (report.fullyCovered) {
    console.log('  覆盖：完全覆盖，无遗漏输入');
  } else {
    console.log(`  覆盖：存在未覆盖输入，最小反例=${formatInput(idx, report.uncoveredWitness!)}`);
    for (const b of report.uncoveredBoxes) {
      console.log(`    未覆盖区域: ${formatBox(idx, b)}`);
    }
  }
}

printReport('初始规则集', analyzer.analyze());

// 增删规则后重新分析
analyzer.removeRule('ent-all');
analyzer.addRule({ id: 'senior-ent', action: 'allow', conditions: [intRange('age', 65, 121), enumEq('tier', 'enterprise')] });
printReport('删除 ent-all、新增 senior-ent 后', analyzer.analyze());
