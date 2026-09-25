import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeNameAndDescription } from '../src/accname.ts';
import { NameEngine } from '../src/engine.ts';
import type { NameResult } from '../src/engine.ts';
import { NodeTree } from '../src/tree.ts';
import { fixtureMetrics } from './fixtures.ts';

const metrics = fixtureMetrics();

/** 独立全量参考：对树中每个节点从零重算（不经过任何缓存）。 */
function freshReference(tree: NodeTree): Map<string, NameResult> {
  const out = new Map<string, NameResult>();
  for (const key of tree.keys()) {
    out.set(key, computeNameAndDescription(tree, metrics, key));
  }
  return out;
}

/** 断言引擎的增量结果与独立全量参考完全一致。 */
function assertMatchesFresh(engine: NameEngine, tree: NodeTree): void {
  const fresh = freshReference(tree);
  for (const [key, expected] of fresh) {
    assert.deepEqual(engine.get(key), expected, `node ${key} 与全量参考不一致`);
  }
}

test('重复 ID：解析取文档序首个；移除首个后落到第二个，与全量参考一致', () => {
  const tree = new NodeTree([
    { key: 'btn', role: 'button', attrs: { 'aria-labelledby': 'dup' } },
    { key: 'first', id: 'dup', text: 'First' },
    { key: 'second', id: 'dup', text: 'Second' },
  ]);
  const engine = new NameEngine(tree, metrics);
  assert.equal(engine.get('btn').name, 'First');
  assertMatchesFresh(engine, tree);

  tree.removeNode('first');
  const notes = engine.sync();
  assert.deepEqual(notes.map((n) => n.key), ['first', 'btn']);
  assert.equal(engine.get('btn').name, 'Second');
  assertMatchesFresh(engine, tree);
});

test('节点移除：被引用的中间节点删除后链回退，与全量参考一致', () => {
  const tree = new NodeTree([
    {
      key: 'root', role: 'group',
      children: [
        { key: 'btn', role: 'button', attrs: { 'aria-labelledby': 'n1' } },
        { key: 't1', id: 'n1', attrs: { 'aria-labelledby': 'n2' }, text: 'One' },
        { key: 't2', id: 'n2', text: 'Two' },
      ],
    },
  ]);
  const engine = new NameEngine(tree, metrics);
  assert.equal(engine.get('btn').name, 'Two');
  assertMatchesFresh(engine, tree);

  tree.removeNode('t2');
  engine.sync();
  // n2 悬空 → t1 回退到自身文本
  assert.equal(engine.get('btn').name, 'One');
  assertMatchesFresh(engine, tree);
});

test('角色改变：禁止命名 ↔ 可命名，与全量参考一致', () => {
  const tree = new NodeTree([
    { key: 'w', role: 'paragraph', text: 'Hello' },
    { key: 'pic', role: 'img', attrs: { alt: 'Pic' } },
    { key: 'host', role: 'button', attrs: { 'aria-labelledby': 'w-ref' } },
    { key: 'wref', id: 'w-ref', role: 'paragraph', text: 'Referenced' },
  ]);
  const engine = new NameEngine(tree, metrics);
  assert.equal(engine.get('w').name, '');
  assert.equal(engine.get('pic').name, 'Pic');
  assertMatchesFresh(engine, tree);

  tree.setRole('w', 'button');
  tree.setRole('pic', 'presentation');
  const notes = engine.sync();
  const byKey = new Map(notes.map((n) => [n.key, n]));
  assert.equal(byKey.get('w')?.after?.name, 'Hello');
  assert.equal(byKey.get('pic')?.after?.name, '');
  assertMatchesFresh(engine, tree);

  // 被引用节点的角色变化不影响其文本贡献。
  tree.setRole('wref', 'button');
  engine.sync();
  assert.equal(engine.get('host').name, 'Referenced');
  assertMatchesFresh(engine, tree);
});

test('嵌套控件替代文本：alt 变更驱动按钮名更新，与全量参考一致', () => {
  const tree = new NodeTree([
    {
      key: 'btn', role: 'button',
      children: [
        { key: 'icon', role: 'img', attrs: { alt: 'Save icon' } },
        { key: 'caption', text: 'Save' },
      ],
    },
  ]);
  const engine = new NameEngine(tree, metrics);
  assert.equal(engine.get('btn').name, 'Save icon Save');
  assertMatchesFresh(engine, tree);

  tree.setAttribute('icon', 'alt', 'Disk');
  const notes = engine.sync();
  assert.deepEqual(notes.map((n) => n.key), ['btn', 'icon']);
  assert.equal(engine.get('btn').name, 'Disk Save');
  assertMatchesFresh(engine, tree);

  // 描述同样纳入全量核验。
  tree.setAttribute('btn', 'aria-describedby', 'cap-desc');
  tree.appendChild('btn', { key: 'desc', id: 'cap-desc', text: 'Writes to disk' });
  engine.sync();
  assert.equal(engine.get('btn').description, 'Writes to disk');
  assertMatchesFresh(engine, tree);
});
