/**
 * Acceptance scenario for the temporal constraint solver.
 *
 *   - Task A must finish before B starts      (A before B)
 *   - B and C partially overlap               (B overlaps C)
 *   - C must start before project day 5       (C.start < 5 days)
 *   - The project must finish by day 9        (C.end <= 9 days)
 *   - D is a 1-day slack task that must finish before C starts
 *
 * The solver propagates the qualitative relations (Allen interval algebra,
 * path consistency = transitive closure), then computes earliest/latest
 * feasible windows, total float and the critical path via an STP, and finally
 * projects everything onto a UTC+8 Monday-Friday 09:00-17:00 calendar.
 *
 * Run with:  node examples/project-schedule.ts   (Node >= 22.6)
 */

import { SchedulingSolver } from '../src/solver.ts';
import { Calendar } from '../src/calendar.ts';

const DAY = 480; // one workday = 480 working minutes (09:00-17:00)

const solver = new SchedulingSolver({ epsilon: 1 });
const A = solver.addTask('A', 2 * DAY);
const B = solver.addTask('B', 3 * DAY);
const C = solver.addTask('C', 4 * DAY);
const D = solver.addTask('D', 1 * DAY);

solver.constrain(A, B, 'before'); // A 必须在 B 之前结束
solver.constrain(B, C, 'overlaps'); // B 与 C 部分重叠
solver.constrain(D, C, 'before'); // D 只需在 C 开始前结束（非关键任务）
solver.require(C, 'start', '<', 5 * DAY); // C 必须在第 5 天前开始
solver.require(C, 'end', '<=', 9 * DAY); // 项目须在第 9 天结束前完成

const result = solver.solve();
if (!result.consistent) {
  console.error('约束网络不可满足！');
  process.exit(1);
}

const ids = { A, B, C, D } as const;
const names = Object.keys(ids) as Array<keyof typeof ids>;

console.log('=== 1. 定性传播（Allen 区间代数 · 传递闭包） ===');
for (const x of names) {
  for (const y of names) {
    if (x >= y) continue;
    console.log(`  ${x} -> ${y}: ${result.relationLabel(ids[x], ids[y])}`);
  }
}
console.log('  推导要点: A before B 且 B overlaps C  =>  A before C（传递闭包自动得出）');

console.log('\n=== 2. 定量窗口（STP，单位 = 工作分钟，1 工作日 = 480） ===');
console.log('  任务   ES     LS     EF     LF     时差    关键');
for (const w of result.windows) {
  const mark = w.critical ? '*' : '';
  console.log(
    `  ${w.task.padEnd(4)}${String(w.earliestStart).padStart(5)} ${String(w.latestStart).padStart(6)} ` +
      `${String(w.earliestFinish).padStart(6)} ${String(w.latestFinish).padStart(6)} ${String(w.float).padStart(6)}    ${mark}`,
  );
}

console.log('\n=== 3. 关键路径 ===');
console.log(`  关键任务: ${result.criticalTasks.join(' -> ')}`);
console.log(
  `  关键路径窗口: [${result.criticalWindow!.start}, ${result.criticalWindow!.end}] 工作分钟` +
    `（第 ${result.criticalWindow!.start / DAY} 天 ~ 第 ${(result.criticalWindow!.end + 1) / DAY} 天）`,
);
console.log(
  `  项目全局窗口: 最早开始 ${result.projectWindow!.earliestStart}，最迟结束 ${result.projectWindow!.latestFinish}`,
);
console.log(`  任务 A 最迟结束时间: 第 5 天前 ${5 * DAY - result.windows[A].latestFinish} 分钟` +
  `（LF = ${result.windows[A].latestFinish}）`);

console.log('\n=== 4. 墙钟投影（UTC+8，周一至周五 09:00-17:00） ===');
const calendar = new Calendar({
  timeZoneOffsetMinutes: 8 * 60,
  workDays: [1, 2, 3, 4, 5],
  dayStartMinutes: 9 * 60,
  dayEndMinutes: 17 * 60,
});
const projectStart = new Date('2026-01-05T01:00:00Z'); // 2026-01-05 09:00 +08:00（周一）
console.log(`  项目启动: ${calendar.format(projectStart)}`);
for (const w of result.windows) {
  const es = calendar.format(calendar.toWallClock(w.earliestStart, projectStart));
  const lf = calendar.format(calendar.toWallClock(w.latestFinish, projectStart));
  console.log(`  ${w.task}: 最早开始 ${es}   最迟结束 ${lf}`);
}
