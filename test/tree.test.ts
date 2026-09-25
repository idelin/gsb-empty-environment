import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fixtureTree } from '../src/fixtures.ts';
import { Server } from '../src/server.ts';
import { applyMove, isValidMove, listRows, removeSubtree, reorderChildren } from '../src/tree.ts';
import type { MoveOp } from '../src/types.ts';

test('禁止移入自身后代', () => {
  const tree = fixtureTree();
  assert.equal(isValidMove(tree, { sourceId: 'a', targetId: 'a1', placement: 'inside' }), false);
  assert.equal(isValidMove(tree, { sourceId: 'a', targetId: 'a2', placement: 'after' }), false);
  assert.equal(isValidMove(tree, { sourceId: 'a', targetId: 'a', placement: 'before' }), false);
  assert.equal(isValidMove(tree, { sourceId: 'a1', targetId: 'a', placement: 'inside' }), true);
  assert.equal(isValidMove(tree, { sourceId: 'a2', targetId: 'b', placement: 'inside' }), true);
});

test('applyMove 不修改原树（不可变）', () => {
  const tree = fixtureTree();
  const snapshot = structuredClone(tree);
  const next = applyMove(tree, { sourceId: 'a2', targetId: 'b', placement: 'inside' });
  assert.deepEqual(tree, snapshot);
  assert.notEqual(next, tree);
  assert.equal(next.version, 1);
  assert.deepEqual(next.nodes.b.childIds, ['b1', 'a2']);
  assert.deepEqual(next.nodes.a.childIds, ['a1']);
  assert.equal(next.nodes.a2.parentId, 'b');
});

test('applyMove 提升相关节点 revision', () => {
  const tree = fixtureTree();
  const next = applyMove(tree, { sourceId: 'a2', targetId: 'b', placement: 'inside' });
  assert.equal(next.nodes.a2.revision, 1);
  assert.equal(next.nodes.b.revision, 1);
  assert.equal(next.nodes.a.revision, 1);
  assert.equal(next.nodes.c.revision, 0);
});

test('removeSubtree 删除整棵子树', () => {
  const next = removeSubtree(fixtureTree(), 'a');
  assert.deepEqual(next.rootIds, ['b', 'c']);
  assert.equal(next.nodes.a, undefined);
  assert.equal(next.nodes.a1, undefined);
  assert.equal(next.nodes.a2, undefined);
  assert.equal(next.version, 1);
});

test('reorderChildren 校验成员一致', () => {
  const tree = fixtureTree();
  const next = reorderChildren(tree, null, ['c', 'b', 'a']);
  assert.deepEqual(next.rootIds, ['c', 'b', 'a']);
  assert.throws(() => reorderChildren(tree, null, ['a', 'b']));
});

test('服务器校验源版本', () => {
  const server = new Server(fixtureTree());
  const op: MoveOp = { opId: 'op-x', sourceId: 'a2', targetId: 'b', placement: 'inside', sourceVersion: 7, targetRevision: 0 };
  const result = server.applyMove(op);
  assert.equal(result.status, 'rejected');
  assert.equal((result as { reason: string }).reason, 'stale-source-version');
});

test('服务器校验目标版本', () => {
  const server = new Server(fixtureTree());
  const op: MoveOp = { opId: 'op-y', sourceId: 'a2', targetId: 'b', placement: 'inside', sourceVersion: 0, targetRevision: 99 };
  const result = server.applyMove(op);
  assert.equal(result.status, 'rejected');
  assert.equal((result as { reason: string }).reason, 'stale-target-revision');
});

test('服务器拒绝缺失目标与非法移动', () => {
  const server = new Server(fixtureTree());
  const missing: MoveOp = { opId: 'op-m', sourceId: 'a2', targetId: 'zzz', placement: 'inside', sourceVersion: 0, targetRevision: 0 };
  assert.equal((server.applyMove(missing) as { reason: string }).reason, 'target-missing');
  const intoDescendant: MoveOp = { opId: 'op-d', sourceId: 'a', targetId: 'a1', placement: 'inside', sourceVersion: 0, targetRevision: 0 };
  assert.equal((server.applyMove(intoDescendant) as { reason: string }).reason, 'invalid-move');
});

test('listRows 深度优先展开', () => {
  assert.deepEqual(
    listRows(fixtureTree()),
    [
      { id: 'a', depth: 0 },
      { id: 'a1', depth: 1 },
      { id: 'a2', depth: 1 },
      { id: 'b', depth: 0 },
      { id: 'b1', depth: 1 },
      { id: 'c', depth: 0 },
    ],
  );
});
