/** 同步导航：方向布局、回环、模态 containment、删除/折叠后的合法后备。 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createApp } from '../src/fixtures.ts';

const MODAL_FOCUSABLES = new Set(['M-CONFIRM', 'M-CANCEL', 'M-L1', 'M-L2', 'M-L3']);

test('方向布局：工具栏内 Left/Right 按逻辑次序移动并回环', () => {
  const { navigator } = createApp();
  navigator.pressKeyEvent('ArrowRight');
  assert.equal(navigator.focusId, 'BTN-EDIT');
  navigator.pressKeyEvent('ArrowRight');
  assert.equal(navigator.focusId, 'BTN-VIEW');
  navigator.pressKeyEvent('ArrowRight'); // 回环
  assert.equal(navigator.focusId, 'BTN-FILE');
  navigator.pressKeyEvent('ArrowLeft'); // 反向回环
  assert.equal(navigator.focusId, 'BTN-VIEW');
});

test('空间导航：工具栏向下进入侧栏', () => {
  const { navigator } = createApp();
  navigator.pressKeyEvent('ArrowDown');
  assert.equal(navigator.focusId, 'S1');
  navigator.pressKeyEvent('ArrowDown');
  assert.equal(navigator.focusId, 'S2');
});

test('模态：打开后焦点自动进入，且方向键不能越界', () => {
  const { tree, navigator } = createApp();
  navigator.focusById('OPEN');
  tree.setVisible('MODAL', true);
  // 背景被遮罩遮挡，焦点救援进入模态第一个可聚焦节点
  assert.equal(navigator.focusId, 'M-CONFIRM');

  navigator.pressKeyEvent('ArrowRight');
  assert.equal(navigator.focusId, 'M-CANCEL');
  navigator.pressKeyEvent('ArrowRight'); // 模态内回环
  assert.equal(navigator.focusId, 'M-CONFIRM');
  navigator.pressKeyEvent('ArrowLeft'); // 模态内反向回环
  assert.equal(navigator.focusId, 'M-CANCEL');
  navigator.pressKeyEvent('ArrowUp'); // 顶部向上 → 回环到底部，仍在模态内
  assert.ok(MODAL_FOCUSABLES.has(navigator.focusId));

  // 连续多方向乱序按键：焦点永不越出模态
  for (const key of ['ArrowDown', 'ArrowDown', 'ArrowLeft', 'ArrowUp', 'ArrowRight', 'ArrowDown']) {
    navigator.pressKeyEvent(key);
  }
  assert.ok(MODAL_FOCUSABLES.has(navigator.focusId));
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('模态：关闭后焦点合法后备到触发按钮', () => {
  const { tree, navigator } = createApp();
  navigator.focusById('OPEN');
  tree.setVisible('MODAL', true);
  assert.equal(navigator.focusId, 'M-CONFIRM');
  tree.setVisible('MODAL', false);
  assert.equal(navigator.focusId, 'OPEN');
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('当前焦点被删除：后备到逻辑次序上的下一个可聚焦节点', () => {
  const { tree, navigator } = createApp();
  navigator.focusById('C1');
  tree.remove('C1');
  assert.equal(navigator.focusId, 'C2');
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('容器折叠：焦点在折叠子树内时回退到合法后备', () => {
  const { tree, navigator } = createApp();
  navigator.focusById('A1');
  tree.setCollapsed('ADV', true);
  // A1/A2 均不可见，逻辑次序上的下一个可见可聚焦节点是 OPEN
  assert.equal(navigator.focusId, 'OPEN');
  assert.deepEqual(navigator.checkInvariants(), []);
});

test('绝不把焦点交给不可见旧节点：隐藏候选后导航跳过它', () => {
  const { tree, navigator } = createApp();
  tree.setVisible('BTN-EDIT', false);
  navigator.pressKeyEvent('ArrowRight'); // BTN-EDIT 不可见 → 跳到 BTN-VIEW
  assert.equal(navigator.focusId, 'BTN-VIEW');
  assert.deepEqual(navigator.checkInvariants(), []);
});
