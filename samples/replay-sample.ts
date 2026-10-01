/**
 * 可重放样例：嵌套滚动 + 模态区域的方向键焦点导航。
 * 运行：node --experimental-strip-types samples/replay-sample.ts
 * 退出码 0 = 全部不变量通过；1 = 存在违例。
 */

import { createApp } from '../src/fixtures.ts';
import { replay } from '../src/replay.ts';
import { buildScenario } from './scenario.ts';

const app = createApp();
const report = replay(
  { navigator: app.navigator, scheduler: app.scheduler },
  buildScenario(app),
);

console.log('=== 回放日志 ===');
for (const line of report.log) console.log(line);
console.log('\n=== 决策轨迹 ===');
for (const line of app.navigator.trace) console.log(line);
console.log('\n=== 读屏公告 ===');
for (const line of app.navigator.announcements) console.log(`announce: ${line}`);

if (!report.ok) {
  console.error('\n=== 违例 ===');
  for (const v of report.violations) console.error(v);
  process.exit(1);
}
console.log('\nOK: 无焦点丢失、无循环等待、读屏焦点同步。');
