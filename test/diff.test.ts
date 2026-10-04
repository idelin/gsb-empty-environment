import { test } from 'node:test';
import assert from 'node:assert/strict';
import { diffSnapshots } from '../src/index.ts';
import { dir, file } from './helpers.ts';

test('识别新增、删除、改名、移动、内容变化', () => {
  const source = [
    dir('root', null, 'root'),
    dir('d1', 'root', 'docs'),
    file('f1', 'd1', 'a.txt', 'v1'),
    file('f2', 'd1', 'b.txt', 'v1'),
    file('f3', 'root', 'c.txt', 'v1'),
    file('f4', 'root', 'gone.txt', 'v1'),
  ];
  const target = [
    dir('root', null, 'root'),
    dir('d1', 'root', 'docs'),
    dir('d2', 'root', 'assets'),          // 新增目录
    file('f1', 'd1', 'a-renamed.txt', 'v1'), // 改名
    file('f2', 'root', 'b.txt', 'v1'),    // 移动 d1 -> root
    file('f3', 'root', 'c.txt', 'v2'),    // 内容变化
    file('f5', 'd2', 'new.png', 'v1'),    // 新增文件
  ];
  const diff = diffSnapshots(source, target);

  assert.deepEqual(diff.added.map((n) => n.id).sort(), ['d2', 'f5']);
  assert.deepEqual(diff.removed.map((n) => n.id), ['f4']);
  assert.deepEqual(diff.renamed.map((r) => [r.node.id, r.fromName, r.node.name]), [
    ['f1', 'a.txt', 'a-renamed.txt'],
  ]);
  assert.deepEqual(diff.moved.map((m) => m.node.id), ['f2']);
  assert.deepEqual(diff.contentChanged.map((c) => [c.node.id, c.fromDigest, c.node.digest]), [
    ['f3', 'v1', 'v2'],
  ]);
});

test('相同 id 搬移不能报成删除+新增', () => {
  const source = [dir('root', null, 'root'), dir('d1', 'root', 'a'), dir('d2', 'root', 'b'), file('f', 'd1', 'x.txt', 'v1')];
  const target = [dir('root', null, 'root'), dir('d1', 'root', 'a'), dir('d2', 'root', 'b'), file('f', 'd2', 'x.txt', 'v1')];
  const diff = diffSnapshots(source, target);
  assert.equal(diff.added.length, 0);
  assert.equal(diff.removed.length, 0);
  assert.equal(diff.moved.length, 1);
  assert.equal(diff.moved[0]!.node.id, 'f');
});

test('id 复用必须依代际区分：同 id 不同代际 = 删除+新增', () => {
  const source = [dir('root', null, 'root'), file('f', 'root', 'config.json', 'v1', 0)];
  const target = [dir('root', null, 'root'), file('f', 'root', 'config.json', 'v9', 1)];
  const diff = diffSnapshots(source, target);
  assert.equal(diff.renamed.length, 0);
  assert.equal(diff.contentChanged.length, 0);
  assert.deepEqual(diff.removed.map((n) => `${n.id}@${n.generation}`), ['f@0']);
  assert.deepEqual(diff.added.map((n) => `${n.id}@${n.generation}`), ['f@1']);
});

test('同键但类型变化（文件变目录）视为删除+新增', () => {
  const source = [dir('root', null, 'root'), file('x', 'root', 'x', 'v1')];
  const target = [dir('root', null, 'root'), dir('x', 'root', 'x')];
  const diff = diffSnapshots(source, target);
  assert.deepEqual(diff.removed.map((n) => n.id), ['x']);
  assert.deepEqual(diff.added.map((n) => n.id), ['x']);
});

test('无变化时差异为空', () => {
  const snap = [dir('root', null, 'root'), file('f', 'root', 'a', 'v1')];
  const diff = diffSnapshots(snap, snap);
  assert.equal(diff.added.length + diff.removed.length + diff.renamed.length + diff.moved.length + diff.contentChanged.length, 0);
});
