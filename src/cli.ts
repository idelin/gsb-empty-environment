#!/usr/bin/env node
/**
 * 用法: node src/cli.ts <config.json>
 *   config.json: { "rules": [{id, action, pattern}...], "samples": [{id, input, expect}...] }
 * 省略参数时从 stdin 读取。
 */
import { readFileSync } from 'node:fs';
import { solve, type Rule, type Sample } from './solver.ts';

function main(): void {
  const arg = process.argv[2];
  const raw = arg ? readFileSync(arg, 'utf8') : readFileSync(0, 'utf8');
  const config = JSON.parse(raw) as { rules: Rule[]; samples: Sample[] };

  const result = solve(config.rules, config.samples);

  if (result.ok) {
    console.log(`可满足，最少相邻交换次数: ${result.swaps}`);
    console.log('新顺序（优先级从高到低）:');
    result.order.forEach((id, i) => console.log(`  ${i + 1}. ${id}`));
    console.log('样例命中:');
    for (const h of result.hits) {
      console.log(`  [${h.sampleId}] "${h.input}" 期望 ${h.expect} -> 命中规则 ${h.hitRuleId}`);
    }
  } else {
    console.log(`无解: ${result.reason}`);
    console.log(`极小冲突样例子集（${result.conflictSamples.length} 条）:`);
    for (const s of result.conflictSamples) {
      console.log(`  [${s.id}] input="${s.input}" expect=${s.expect}`);
    }
    process.exitCode = 1;
  }
}

main();
