/**
 * 异步导航：慢挂载令牌过期、滚动完成时的布局版本校验、
 * 候选删除重规划、滚动后几何改变、无循环等待。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createApp } from '../src/fixtures.ts';

test('连续两次按键使首次慢挂载结果过期', () => {
  const { scheduler, navigator } = createApp();
  navigator.focusById('S6');
  navigator.pressKeyEvent('ArrowDown'); // token=2：请求挂载 S7（100ms）
  navigator.pressKeyEvent('ArrowDown'); // token=3：首次结果应过期
  assert.equal(navigator.focusId, 'S6'); // 挂载未完成，焦点不动（无焦点丢失）
  scheduler.advance(100);
  assert.equal(navigator.focusId, 'S7'); // 仅第二次的结果生效
  const stale = navigator.trace.filter((l) => l.includes('STALE'));
  assert.equal(stale.length, 1); // 首次完成回调被丢弃
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('先确定候选再请求滚动，完成时校验布局版本', () => {
  const { tree, scheduler, navigator } = createApp();
  tree.setMounted('S7', true); // 预挂载，隔离滚动路径
  navigator.focusById('S7');
  navigator.pressKeyEvent('ArrowDown'); // S8 在视口外 → 请求滚动
  assert.equal(navigator.focusId, 'S7'); // 滚动未完成，焦点不动
  assert.ok(navigator.trace.some((l) => l.startsWith('scroll-request SIDEBAR for S8')));
  scheduler.advance(20);
  assert.equal(navigator.focusId, 'S8');
  // 滚动后几何改变：S8 有效矩形进入视口
  const rect = tree.effectiveRect(tree.get('S8'));
  assert.ok(rect.y + rect.h <= 400);
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('滚动等待期间布局被外部改变：完成时版本不符 → 重规划', () => {
  const { tree, scheduler, navigator } = createApp();
  tree.setMounted('S7', true);
  navigator.focusById('S7');
  navigator.pressKeyEvent('ArrowDown'); // 请求滚动到 S8
  tree.setVisible('C1', false); // 外部变更：布局版本 +1
  scheduler.advance(20); // 版本不符 → 重规划 → 重新请求滚动
  assert.ok(navigator.trace.some((l) => l.includes('layout-changed')));
  assert.equal(navigator.focusId, 'S7'); // 尚未提交
  scheduler.advance(20); // 第二次滚动完成
  assert.equal(navigator.focusId, 'S8');
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('候选在异步等待期间被删除：重新规划且不聚焦已删节点', () => {
  const { tree, scheduler, navigator } = createApp();
  navigator.focusById('S6');
  navigator.pressKeyEvent('ArrowDown'); // 请求挂载 S7
  tree.remove('S7'); // 候选被删（布局版本 +1）
  scheduler.advance(100); // 版本不符 → 重规划
  assert.notEqual(navigator.focusId, 'S7');
  assert.ok(navigator.trace.some((l) => l.includes('layout-changed')));
  scheduler.runAll();
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('挂载完成后候选不可见：重规划到下一合法候选', () => {
  const { tree, scheduler, navigator } = createApp();
  navigator.focusById('S6');
  navigator.pressKeyEvent('ArrowDown'); // 请求挂载 S7
  tree.setVisible('S7', false); // 挂载完成前被隐藏
  scheduler.advance(100);
  scheduler.runAll();
  assert.notEqual(navigator.focusId, 'S7');
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('无循环等待：重规划超限落到合法后备，调度器任务有界', () => {
  const { tree, scheduler, navigator } = createApp();
  navigator.maxReplans = 0; // 立即触发上限，验证有界退出
  navigator.focusById('S6');
  navigator.pressKeyEvent('ArrowDown'); // 请求挂载 S7
  tree.remove('S7');
  scheduler.advance(100); // 版本不符 → 重规划 → 超限 → 合法后备
  assert.ok(navigator.trace.some((l) => l.includes('replan-overflow')));
  assert.equal(navigator.focusId, 'BTN-FILE'); // 作用域内第一个可聚焦节点
  scheduler.runAll(); // 若存在循环等待，调度器会抛错
  assert.equal(scheduler.pendingCount, 0);
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('无候选时焦点安全停留（不丢失、不等待）', () => {
  const { scheduler, navigator } = createApp();
  // 模态内只剩一个可聚焦节点时，任意方向都无候选
  navigator.focusById('BTN-FILE');
  for (const id of ['BTN-EDIT', 'BTN-VIEW']) navigator.tree.setVisible(id, false);
  // 隐藏其余全部可聚焦节点，仅留当前焦点
  for (const id of ['S1','S2','S3','S4','S5','S6','S7','S8','C1','C2','C3','H1','H2','H3','H4','H5','H6','A1','A2','OPEN']) {
    navigator.tree.setVisible(id, false);
  }
  navigator.pressKeyEvent('ArrowRight');
  navigator.pressKeyEvent('ArrowDown');
  scheduler.runAll();
  assert.equal(navigator.focusId, 'BTN-FILE');
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('按键风暴 + 异步混合：调度器可排空，无焦点丢失', () => {
  const { tree, scheduler, navigator } = createApp();
  navigator.focusById('S6');
  for (let i = 0; i < 20; i++) {
    navigator.pressKeyEvent(i % 2 ? 'ArrowDown' : 'ArrowUp');
  }
  tree.setVisible('MODAL', true);
  for (let i = 0; i < 10; i++) navigator.pressKeyEvent('ArrowRight');
  scheduler.runAll();
  assert.equal(scheduler.pendingCount, 0);
  assert.deepEqual(navigator.checkInvariants(), []);
});
