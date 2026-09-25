import { test } from 'node:test';
import assert from 'node:assert/strict';
import { NodeTree } from '../src/tree.ts';
import { screenSpec, baseMetrics, glyphsFor } from '../fixtures/workbench.fixture.ts';

function makeTree(): NodeTree {
  return new NodeTree(screenSpec(), baseMetrics());
}

test('文本叶子尺寸由字形度量推导', () => {
  const tree = makeTree();
  assert.deepEqual(tree.intrinsicSize('txt-hero'), { width: 5 * 14, height: 24 });
  assert.ok(tree.isVisible('txt-hero'));
});

test('盒塌陷与 display:none 的节点不可见、不可聚焦', () => {
  const tree = makeTree();
  assert.ok(!tree.isVisible('btn-ghost'));
  assert.ok(!tree.isFocusable('btn-ghost'));
  assert.ok(!tree.isVisible('btn-hidden'));
  assert.ok(!tree.isFocusable('btn-hidden'));
  assert.ok(tree.isFocusable('btn-open-a'));
});

test('inert 引用计数：重复施加需等量释放，沿祖先链传播', () => {
  const tree = makeTree();
  tree.addInertClaim('main');
  tree.addInertClaim('main');
  assert.equal(tree.inertClaimCount('main'), 2);
  assert.ok(tree.isInert('btn-open-a'));
  assert.ok(!tree.isFocusable('btn-open-a'));
  assert.ok(!tree.isInert('btn-fallback'));
  tree.removeInertClaim('main');
  assert.ok(tree.isInert('btn-open-a'));
  tree.removeInertClaim('main');
  assert.ok(!tree.isInert('btn-open-a'));
  assert.equal(tree.totalInertClaims(), 0);
  tree.removeInertClaim('main');
  assert.equal(tree.totalInertClaims(), 0);
});

test('卸载子树时其上的 inert 引用一并释放', () => {
  const tree = makeTree();
  tree.addInertClaim('main');
  tree.addInertClaim('footer');
  assert.equal(tree.totalInertClaims(), 2);
  tree.unmount('main');
  assert.equal(tree.totalInertClaims(), 1);
  assert.ok(!tree.has('btn-open-a'));
});

test('firstFocusableWithin 跳过隐藏与 inert 子树', () => {
  const tree = makeTree();
  assert.equal(tree.firstFocusableWithin('app-root'), 'btn-nav-home');
  tree.addInertClaim('header');
  assert.equal(tree.firstFocusableWithin('app-root'), 'btn-open-a');
  assert.deepEqual(tree.collectFocusableWithin('main'), ['btn-open-a', 'btn-open-b']);
});

test('挂载/卸载触变监听，重复 id 拒绝', () => {
  const tree = makeTree();
  let mutations = 0;
  tree.onMutation(() => {
    mutations += 1;
  });
  tree.mount('main', { id: 'btn-extra', focusable: true, text: '额外' }, {
    'btn-extra': { glyphs: glyphsFor('额外'), lineHeight: 20 },
  });
  assert.equal(mutations, 1);
  assert.ok(tree.isFocusable('btn-extra'));
  assert.throws(() => tree.mount('main', { id: 'btn-extra' }), /duplicate/);
  tree.unmount('btn-extra');
  assert.equal(mutations, 2);
  assert.equal(tree.unmount('btn-extra'), false);
});
