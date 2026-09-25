import { test } from 'node:test';
import assert from 'node:assert/strict';
import { NameEngine } from '../src/engine.ts';
import { NodeTree } from '../src/tree.ts';
import { fixtureMetrics } from './fixtures.ts';

const metrics = fixtureMetrics();

function sharedLabelTree(): NodeTree {
  return new NodeTree([
    { key: 'inputX', role: 'textbox', attrs: { 'aria-labelledby': 'shared' } },
    { key: 'inputY', role: 'textbox', attrs: { 'aria-labelledby': 'shared' } },
    { key: 'lbl', id: 'shared', text: 'User name' },
    { key: 'other', role: 'button', text: 'Unrelated' },
  ]);
}

test('修改被多处引用的标签：全部相关节点收到通知，无关节点不重算', () => {
  const tree = sharedLabelTree();
  const engine = new NameEngine(tree, metrics);
  assert.equal(engine.get('inputX').name, 'User name');
  assert.equal(engine.get('inputY').name, 'User name');
  assert.equal(engine.get('other').name, 'Unrelated');
  const computedBefore = engine.stats.computed;

  tree.setText('lbl', 'Email address');
  const notes = engine.sync();

  assert.deepEqual(notes.map((n) => n.key), ['inputX', 'inputY']);
  assert.equal(notes[0]?.before?.name, 'User name');
  assert.equal(notes[0]?.after?.name, 'Email address');
  assert.equal(notes[1]?.after?.name, 'Email address');
  assert.equal(engine.stats.recomputed, 2);

  // 无关节点未被重算，仍由缓存服务。
  const statsBefore = { ...engine.stats };
  assert.equal(engine.get('other').name, 'Unrelated');
  assert.equal(engine.stats.computed, statsBefore.computed);
  assert.equal(engine.stats.hits, statsBefore.hits + 1);
  assert.ok(engine.stats.computed >= computedBefore);
});

test('无关文本变更不触发任何通知与重算', () => {
  const tree = sharedLabelTree();
  const engine = new NameEngine(tree, metrics);
  engine.get('inputX');
  engine.get('inputY');

  tree.setText('other', 'Still unrelated');
  const notes = engine.sync();

  assert.deepEqual(notes, []);
  assert.equal(engine.stats.recomputed, 0);
  assert.equal(engine.get('inputX').name, 'User name');
});

test('隐藏状态切换只影响把该节点当内容的父节点，显式引用方不受影响', () => {
  const tree = new NodeTree([
    {
      key: 'container',
      role: 'button',
      children: [
        { key: 'maybe', id: 'm', text: 'Maybe' },
        { key: 'tail', text: 'Tail' },
      ],
    },
    { key: 'ref', role: 'button', attrs: { 'aria-labelledby': 'm' } },
  ]);
  const engine = new NameEngine(tree, metrics);
  assert.equal(engine.get('container').name, 'Maybe Tail');
  assert.equal(engine.get('ref').name, 'Maybe');

  tree.setHidden('maybe', true);
  const notes = engine.sync();
  const byKey = new Map(notes.map((n) => [n.key, n]));
  // 内容遍历跳过未引用的隐藏节点；显式引用仍然计入。
  assert.equal(byKey.get('container')?.after?.name, 'Tail');
  assert.equal(byKey.has('ref'), false);
  assert.equal(engine.get('ref').name, 'Maybe');
});

test('错误引用不串到其他实例：缓存按树版本隔离', () => {
  const treeA = new NodeTree([
    {
      key: 'rootA', role: 'group',
      children: [
        { key: 'btnA', role: 'button', attrs: { 'aria-labelledby': 'ghost' }, text: 'FallbackA' },
      ],
    },
  ]);
  const treeB = new NodeTree([
    { key: 'btnB', role: 'button', attrs: { 'aria-labelledby': 'ghost' } },
    { key: 'targetB', id: 'ghost', text: 'Boo' },
  ]);
  const engineA = new NameEngine(treeA, metrics);
  const engineB = new NameEngine(treeB, metrics);

  // A 中引用悬空：回退到自身内容；B 中同 id 正常解析，互不影响。
  assert.equal(engineA.get('btnA').name, 'FallbackA');
  assert.equal(engineB.get('btnB').name, 'Boo');

  // 在 A 中补上该 id：A 的悬空引用被“治愈”并通知；B 完全不受波及。
  treeA.appendChild('rootA', { key: 'targetA', id: 'ghost', text: 'Now found' });
  const notesA = engineA.sync();
  assert.deepEqual(notesA.map((n) => n.key), ['btnA']);
  assert.equal(notesA[0]?.before?.name, 'FallbackA');
  assert.equal(notesA[0]?.after?.name, 'Now found');

  const statsB = { ...engineB.stats };
  assert.equal(engineB.get('btnB').name, 'Boo');
  assert.equal(engineB.stats.computed, statsB.computed);
  assert.equal(engineB.stats.recomputed, 0);
});

test('被移除节点的缓存条目被清除并通知', () => {
  const tree = new NodeTree([
    { key: 'btn', role: 'button', attrs: { 'aria-labelledby': 'gone' } },
    { key: 'lbl', id: 'gone', text: 'Leaving' },
  ]);
  const engine = new NameEngine(tree, metrics);
  engine.get('btn');
  engine.get('lbl');

  tree.removeNode('lbl');
  const notes = engine.sync();
  const byKey = new Map(notes.map((n) => [n.key, n]));
  assert.equal(byKey.get('lbl')?.after, null);
  assert.equal(byKey.get('btn')?.after?.name, '');
});
