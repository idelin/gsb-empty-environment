/**
 * 验收场景：
 *   - 任务 A 必须在 B 之前结束（A before B）
 *   - B 与 C 部分重叠（B overlaps C）
 *   - C 必须在工期第 5 天前开始（C.start < 5）
 * 求解器传播时态关系，输出每个任务的最早/最晚可行时间窗、
 * 任务 A 的最迟结束时间，以及项目全局关键路径窗口。
 */

import { formatRelationSet } from '../src/allen.ts';
import { Scheduler } from '../src/solver.ts';

const scheduler = new Scheduler();

scheduler
  .addTask('A', 2) // 工期 2 天
  .addTask('B', 3) // 工期 3 天
  .addTask('C', 4); // 工期 4 天

scheduler.addRelation('A', 'before', 'B'); // A 必须在 B 之前结束
scheduler.addRelation('B', 'overlaps', 'C'); // B 与 C 部分重叠
scheduler.startBefore('C', 5); // C 必须在第 5 天前开始

const result = scheduler.solve();

if (!result.consistent) {
  console.log('约束网络不可满足！');
  process.exit(1);
}

console.log('=== 时态约束传播（Allen 区间代数，传递闭包后） ===');
for (const [x, y] of [['A', 'B'], ['B', 'C'], ['A', 'C']] as const) {
  console.log(
    `  ${x} -> ${y}: ${formatRelationSet(result.relationBetween(x, y))}`,
  );
}

console.log('\n=== 任务可行时间窗（单位：天，项目自第 0 天开始） ===');
console.log(
  '  任务  开始窗口        结束窗口        总时差',
);
for (const t of result.tasks) {
  const sw = `[${t.earliestStart}, ${t.latestStart}]`;
  const ew = `[${t.earliestEnd}, ${t.latestEnd}]`;
  console.log(
    `  ${t.name.padEnd(4)}  ${sw.padEnd(14)}  ${ew.padEnd(14)}  ${t.float}`,
  );
}

const a = result.tasks.find((t) => t.name === 'A')!;
console.log(`\n任务 A 的最迟结束时间: 第 ${a.latestEnd} 天`);

const [minEnd, maxEnd] = result.projectWindow!;
console.log(`项目全局关键路径窗口: [${minEnd}, ${maxEnd}] 天`);
console.log(`关键路径上的任务: ${result.criticalPath.join(' -> ')}`);
