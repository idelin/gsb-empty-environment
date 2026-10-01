/** 键盘事件回放：无焦点丢失、无循环等待、读屏焦点同步。 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createApp } from '../src/fixtures.ts';
import { replay } from '../src/replay.ts';
import { buildScenario } from '../samples/scenario.ts';

test('完整场景回放：全部不变量通过', () => {
  const app = createApp();
  const report = replay(
    { navigator: app.navigator, scheduler: app.scheduler },
    buildScenario(app),
  );
  assert.deepEqual(report.violations, []);
  assert.ok(report.ok);
  assert.equal(app.scheduler.pendingCount, 0); // 无遗留等待（无循环等待）
  assert.deepEqual(app.navigator.checkInvariants(), []);
});

test('读屏焦点同步：公告流与焦点提交一一对应', () => {
  const app = createApp();
  replay({ navigator: app.navigator, scheduler: app.scheduler }, buildScenario(app));
  const commits = app.navigator.trace.filter((l) => l.startsWith('focus -> '));
  // 公告数 = 初始焦点 + 每次提交
  assert.equal(app.navigator.announcements.length, commits.length + 1);
  // 每条公告的节点与对应提交一致
  commits.forEach((line, i) => {
    const id = line.slice('focus -> '.length).split(' ')[0];
    assert.ok(
      app.navigator.announcements[i + 1].startsWith(`${id}:`),
      `announcement ${i + 1} mismatch: ${app.navigator.announcements[i + 1]} vs ${id}`,
    );
  });
});

test('回放期间焦点从未落在不可见/未挂载节点上', () => {
  const app = createApp();
  const report = replay(
    { navigator: app.navigator, scheduler: app.scheduler },
    buildScenario(app),
  );
  // checkInvariants 已在每个步骤后执行；此处再确认无任何违例被记录
  assert.ok(report.ok);
  // 最终焦点可见性复核
  const focus = app.tree.get(app.navigator.focusId);
  assert.ok(app.tree.isEffectivelyVisible(focus));
});
