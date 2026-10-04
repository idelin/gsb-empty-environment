import { test } from 'node:test';
import assert from 'node:assert/strict';
import { diffSnapshots } from '../src/diff.ts';
import { dir, file, snap } from './helpers.ts';

test('detects added, deleted, renamed, moved and content-changed nodes', () => {
  const oldSnap = snap(
    dir('root', null, 'root'),
    file('keep', 'root', 'keep.txt', 'aaa'),
    file('renamed', 'root', 'old-name.txt', 'bbb'),
    file('moved', 'root', 'moved.txt', 'ccc'),
    file('changed', 'root', 'changed.txt', 'ddd'),
    file('gone', 'root', 'gone.txt', 'eee'),
    dir('sub', 'root', 'sub'),
  );
  const newSnap = snap(
    dir('root', null, 'root'),
    file('keep', 'root', 'keep.txt', 'aaa'),
    file('renamed', 'root', 'new-name.txt', 'bbb'),
    file('moved', 'sub', 'moved.txt', 'ccc'),
    file('changed', 'root', 'changed.txt', 'ddd2'),
    file('born', 'root', 'born.txt', 'fff'),
    dir('sub', 'root', 'sub'),
  );

  const changes = diffSnapshots(oldSnap, newSnap);
  const byKind = new Map(changes.map((c) => [c.kind, c]));

  assert.equal(changes.length, 5);
  assert.deepEqual(changes.map((c) => c.kind).sort(), [
    'added',
    'content-changed',
    'deleted',
    'moved',
    'renamed',
  ]);

  const added = byKind.get('added');
  assert.equal(added?.kind, 'added');
  if (added?.kind === 'added') assert.equal(added.node.id, 'born');

  const deleted = byKind.get('deleted');
  assert.equal(deleted?.kind, 'deleted');
  if (deleted?.kind === 'deleted') assert.equal(deleted.node.id, 'gone');

  const renamed = byKind.get('renamed');
  assert.deepEqual(
    renamed && { id: renamed.id, oldName: renamed.kind === 'renamed' ? renamed.oldName : null },
    { id: 'renamed', oldName: 'old-name.txt' },
  );

  const moved = byKind.get('moved');
  assert.equal(moved?.kind, 'moved');
  if (moved?.kind === 'moved') {
    assert.equal(moved.id, 'moved');
    assert.deepEqual(moved.oldParent, { id: 'root', generation: 1 });
    assert.deepEqual(moved.newParent, { id: 'sub', generation: 1 });
  }

  const changed = byKind.get('content-changed');
  assert.equal(changed?.kind, 'content-changed');
  if (changed?.kind === 'content-changed') {
    assert.equal(changed.id, 'changed');
    assert.equal(changed.oldDigest, 'ddd');
    assert.equal(changed.newDigest, 'ddd2');
  }
});

test('a moved node is not reported as delete + add', () => {
  const oldSnap = snap(dir('root', null, 'root'), dir('sub', 'root', 'sub'), file('f', 'root', 'f.txt', 'x'));
  const newSnap = snap(dir('root', null, 'root'), dir('sub', 'root', 'sub'), file('f', 'sub', 'f.txt', 'x'));

  const changes = diffSnapshots(oldSnap, newSnap);
  assert.deepEqual(changes.map((c) => c.kind), ['moved']);
  assert.equal(changes[0].kind === 'moved' && changes[0].id, 'f');
});

test('id reuse across generations is delete + add, not a modification', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('cfg', 'root', 'cfg.json', 'old', 1));
  const newSnap = snap(dir('root', null, 'root'), file('cfg', 'root', 'cfg.json', 'new', 2));

  const changes = diffSnapshots(oldSnap, newSnap);
  assert.deepEqual(changes.map((c) => c.kind).sort(), ['added', 'deleted']);
  const added = changes.find((c) => c.kind === 'added');
  const deleted = changes.find((c) => c.kind === 'deleted');
  assert.equal(added?.kind === 'added' && added.node.generation, 2);
  assert.equal(deleted?.kind === 'deleted' && deleted.node.generation, 1);
});

test('parent id reuse (generation bump) counts as a move for unchanged children', () => {
  const oldSnap = snap(dir('p', null, 'p', 1), file('c', 'p', 'c.txt', 'x'));
  const newSnap = snap(dir('p', null, 'p', 2), file('c', 'p', 'c.txt', 'x'));

  const changes = diffSnapshots(oldSnap, newSnap);
  const moved = changes.find((c) => c.kind === 'moved');
  assert.ok(moved, 'expected a moved change for the child');
  if (moved?.kind === 'moved') {
    assert.equal(moved.id, 'c');
    assert.deepEqual(moved.oldParent, { id: 'p', generation: 1 });
    assert.deepEqual(moved.newParent, { id: 'p', generation: 2 });
  }
  assert.ok(changes.some((c) => c.kind === 'deleted' && c.node.id === 'p'));
  assert.ok(changes.some((c) => c.kind === 'added' && c.node.id === 'p'));
});

test('type change with same identity is delete + add', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('x', 'root', 'x', 'd'));
  const newSnap = snap(dir('root', null, 'root'), dir('x', 'root', 'x'));

  const changes = diffSnapshots(oldSnap, newSnap);
  assert.deepEqual(changes.map((c) => c.kind).sort(), ['added', 'deleted']);
});
