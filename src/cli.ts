import { readFileSync } from 'node:fs';
import { planRelease, PlanInputError } from './planner.ts';
import type { PlanInput } from './types.ts';

/** 用法：node src/cli.ts <input.json>（或省略参数从标准输入读取） */
function readInput(): string {
  const path = process.argv[2];
  if (path && path !== '-') return readFileSync(path, 'utf8');
  return readFileSync(0, 'utf8');
}

function main(): void {
  let input: PlanInput;
  try {
    input = JSON.parse(readInput()) as PlanInput;
  } catch (error) {
    console.error(`输入不是合法 JSON：${(error as Error).message}`);
    process.exit(1);
  }
  let result;
  try {
    result = planRelease(input);
  } catch (error) {
    if (error instanceof PlanInputError) {
      console.error(`输入不合法：${error.message}`);
      process.exit(1);
    }
    throw error;
  }

  if (!result.ok) {
    const w = result.witness;
    console.log('不可行：不存在满足约束的发布计划。');
    console.log(`见证：${w.message}`);
    console.log(`环上成员：${w.members.join('、')}（共 ${w.size} 个，容量上限 ${w.capacity}）`);
    process.exit(2);
  }

  console.log(`发布计划：共 ${result.batchCount} 批（每批容量上限 ${input.capacity}）`);
  for (const batch of result.batches) {
    console.log(`\n第 ${batch.index} 批（${batch.projects.length} 个项目）：${batch.projects.join('、')}`);
    for (const reason of batch.reasons) console.log(`  - ${reason}`);
  }
}

main();
