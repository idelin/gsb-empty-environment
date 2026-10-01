/** 候选选择：公开主轴优先规则、斜向候选、回环、遮挡过滤、逻辑次序平局。 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FocusTree } from '../src/tree.ts';
import { FocusNavigator } from '../src/navigator.ts';
import { Scheduler } from '../src/scheduler.ts';
import { measureText } from '../src/metrics.ts';
import { glyphMetrics, buildAppTree } from '../src/fixtures.ts';
import type { NodeInit } from '../src/tree.ts';

function makeNav(children: NodeInit[], initial: string) {
  const tree = new FocusTree({ id: 'ROOT', rect: { x: 0, y: 0, w: 1000, h: 1000 }, children });
  const scheduler = new Scheduler();
  const nav = new FocusNavigator(tree, scheduler, initial);
  return { tree, scheduler, nav };
}

test('主轴优先：斜向候选按主轴位移参与竞争', () => {
  // A 正右方（主轴位移 100，副轴 0）；B 斜向右下（主轴 20，副轴 50）。
  // 公开规则：先比主轴位移，B (20) < A (100)，故 B 胜出。
  const { nav } = makeNav(
    [
      { id: 'CUR', rect: { x: 0, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'A', rect: { x: 100, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'B', rect: { x: 20, y: 50, w: 10, h: 10 }, focusable: true },
    ],
    'CUR',
  );
  nav.pressKey('right');
  assert.equal(nav.focusId, 'B');
});

test('主轴位移相同时按副轴偏移裁决', () => {
  const { nav } = makeNav(
    [
      { id: 'CUR', rect: { x: 0, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'NEAR', rect: { x: 50, y: 4, w: 10, h: 10 }, focusable: true },
      { id: 'FAR', rect: { x: 50, y: 60, w: 10, h: 10 }, focusable: true },
    ],
    'CUR',
  );
  nav.pressKey('right');
  assert.equal(nav.focusId, 'NEAR');
});

test('回环：主轴正方向无候选时绕到反向极值', () => {
  const { nav } = makeNav(
    [
      { id: 'CUR', rect: { x: 500, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'LEFTMOST', rect: { x: 0, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'MID', rect: { x: 200, y: 0, w: 10, h: 10 }, focusable: true },
    ],
    'CUR',
  );
  nav.pressKey('right'); // 右侧无候选 → 回环到最左
  assert.equal(nav.focusId, 'LEFTMOST');
  nav.pressKey('left'); // 左侧无候选 → 回环到最右
  assert.equal(nav.focusId, 'CUR');
});

test('遮挡：被不透明层覆盖的候选不参与竞争', () => {
  const { nav } = makeNav(
    [
      { id: 'CUR', rect: { x: 0, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'COVERED', rect: { x: 50, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'OPEN', rect: { x: 100, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'MASK', rect: { x: 40, y: -10, w: 30, h: 30 }, opaque: true, z: 10 },
    ],
    'CUR',
  );
  nav.pressKey('right');
  assert.equal(nav.focusId, 'OPEN'); // COVERED 被 MASK 遮挡，跳过
});

test('逻辑次序：几何完全平局时按文档序', () => {
  const { nav } = makeNav(
    [
      { id: 'CUR', rect: { x: 0, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'SECOND', rect: { x: 50, y: 0, w: 10, h: 10 }, focusable: true },
      { id: 'FIRST', rect: { x: 50, y: 0, w: 10, h: 10 }, focusable: true },
    ],
    'CUR',
  );
  // FIRST 文档序在 SECOND 之后定义？FIRST 先出现于 children？此处 SECOND 先定义。
  nav.pressKey('right');
  assert.equal(nav.focusId, 'SECOND'); // 平局取逻辑次序小者
});

test('字形度量：夹具自算文本宽度，不借浏览器布局引擎', () => {
  assert.equal(measureText('File', glyphMetrics), 7 + 3 + 3 + 6);
  assert.equal(measureText('View', glyphMetrics), 8 + 3 + 6 + 9);
  const tree = buildAppTree();
  assert.equal(tree.get('BTN-FILE').rect.w, 19 + 16); // 文本宽 + 两侧 padding
  assert.equal(tree.get('BTN-VIEW').rect.w, 26 + 16);
});
