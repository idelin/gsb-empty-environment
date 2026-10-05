// 命令行入口：
//   node scripts/tsrun.js src/cli.ts [input.json] [--text]
// 不带文件参数时从标准输入读取 JSON。

import * as fs from 'fs';
import { planRelease, PlanInputError } from './planner';
import type { PlanRequest, PlanResult } from './model';

function readInput(): string {
  const args = process.argv.slice(2).filter((a) => a !== '--text');
  if (args.length > 0) return fs.readFileSync(args[0], 'utf8');
  return fs.readFileSync(0, 'utf8');
}

function printText(result: PlanResult): void {
  if (!result.ok) {
    console.log(`不可行：${result.witness.detail}`);
    return;
  }
  console.log(`最少批次数：${result.batchCount}`);
  for (const b of result.batches) {
    console.log(`  第 ${b.index + 1} 批: ${b.projects.join(', ')}`);
    for (const r of b.reasons) {
      console.log(`    [${r.kind}] ${r.projects.join(', ')} — ${r.detail}`);
    }
  }
}

function main(): void {
  const req = JSON.parse(readInput()) as PlanRequest;
  try {
    const result = planRelease(req);
    if (process.argv.indexOf('--text') >= 0) printText(result);
    else console.log(JSON.stringify(result, null, 2));
    if (!result.ok) process.exitCode = 1;
  } catch (e) {
    if (e instanceof PlanInputError) {
      console.log(JSON.stringify({ ok: false, error: (e as Error).message }, null, 2));
      process.exitCode = 2;
      return;
    }
    throw e;
  }
}

main();
