import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ApplyError, TreeApplier, buildPlan } from '../src/index.ts';
import type { PlanOp, SnapshotNode } from '../src/index.ts';
import { dir, file } from './helpers.ts';

function roundTrip(source: SnapshotNode[], target: SnapshotNode[]): TreeApplier {
  const { ops } = buildPlan(source, target);
  const applier = new TreeApplier(source);
  applier.applyAndVerify(ops, target);
  return applier;
}

test('端到端：混合增删改移后结果与目标树一致', () => {
  const source = [
    dir('root', null, 'root'),
    dir('d1', 'root', 'src'),
    file('f1', 'd1', 'a.ts', 'v1'),
    file('f2', 'd1', 'b.ts', 'v1'),
    file('f3', 'root', 'README.md', 'v1'),
  ];
  const target = [
    dir('root', null, 'root'),
    dir('d1', 'root', 'source'),          // 目录改名
    dir('d2', 'd1', 'util'),              // 新增子目录
    file('f1', 'd2', 'a.ts', 'v2'),       // 移动 + 改内容
    file('f3', 'root', 'README.md', 'v1'),
    file('f4', 'root', 'LICENSE', 'v1'),  // 新增
  ];
  roundTrip(source, target);
});

test('互换改名（a<->b）经临时路径解决，不死锁不误报', () => {
  const source = [dir('root', null, 'root'), file('a', 'root', 'one.txt', 'v1'), file('b', 'root', 'two.txt', 'v1')];
  const target = [dir('root', null, 'root'), file('a', 'root', 'two.txt', 'v1'), file('b', 'root', 'one.txt', 'v1')];
  const applier = roundTrip(source, target);
  const names = applier.toSnapshot().filter((n) => n.type === 'file').map((n) => `${n.id}:${n.name}`).sort();
  assert.deepEqual(names, ['a:two.txt', 'b:one.txt']);
});

test('新文件占用待删文件的名字：应用器临时挪走占用者', () => {
  const source = [dir('root', null, 'root'), file('old', 'root', 'config.yml', 'v1')];
  const target = [dir('root', null, 'root'), file('new', 'root', 'config.yml', 'v2')];
  roundTrip(source, target);
});

test('id 复用（目录重建）端到端：旧代际删除、新代际创建', () => {
  const source = [dir('root', null, 'root'), dir('d', 'root', 'data', 0), file('f', 'd', 'a.txt', 'v1')];
  const target = [dir('root', null, 'root'), dir('d', 'root', 'data', 1), file('g', 'd', 'b.txt', 'v1')];
  const applier = roundTrip(source, target);
  const keys = applier.toSnapshot().map((n) => `${n.id}@${n.generation}`).sort();
  assert.deepEqual(keys, ['d@1', 'g@0', 'root@0']);
});

test('前置校验：对错误的源树应用计划会失败', () => {
  const source = [dir('root', null, 'root'), file('f', 'root', 'a.txt', 'v1')];
  const target = [dir('root', null, 'root'), file('f', 'root', 'b.txt', 'v1')];
  const { ops } = buildPlan(source, target);
  const wrongSource = [dir('root', null, 'root'), file('f', 'root', 'other.txt', 'v1')];
  const applier = new TreeApplier(wrongSource);
  assert.throws(() => applier.apply(ops), ApplyError);
});

test('目标槽位被永不腾空的节点占用：报临时路径冲突', () => {
  const source = [dir('root', null, 'root'), file('a', 'root', 'x.txt', 'v1'), file('b', 'root', 'y.txt', 'v1')];
  // 手工构造非法计划：把 a 改名为 b 的名字，但 b 没有任何后续操作
  const ops: PlanOp[] = [{ kind: 'rename', key: 'a@0', fromName: 'x.txt', toName: 'y.txt' }];
  const applier = new TreeApplier(source);
  assert.throws(() => applier.apply(ops), /占用且其不会腾空/);
});

test('删除非空目录被拒绝', () => {
  const source = [dir('root', null, 'root'), dir('d', 'root', 'data'), file('f', 'd', 'a.txt', 'v1')];
  const ops: PlanOp[] = [{ kind: 'remove', key: 'd@0', type: 'dir', name: 'data', parentKey: 'root@0' }];
  const applier = new TreeApplier(source);
  assert.throws(() => applier.apply(ops), /目录非空/);
});

test('verifyAgainst 报告与目标树的差异', () => {
  const source = [dir('root', null, 'root'), file('f', 'root', 'a.txt', 'v1')];
  const applier = new TreeApplier(source);
  const mismatches = applier.verifyAgainst([dir('root', null, 'root'), file('f', 'root', 'a.txt', 'v2')]);
  assert.equal(mismatches.length, 1);
  assert.match(mismatches[0]!, /摘要不一致/);
});

test('内容摘要前置校验：源摘要被篡改时拒绝更新', () => {
  const source = [dir('root', null, 'root'), file('f', 'root', 'a.txt', 'v1')];
  const ops: PlanOp[] = [{ kind: 'update', key: 'f@0', fromDigest: 'tampered', toDigest: 'v2' }];
  const applier = new TreeApplier(source);
  assert.throws(() => applier.apply(ops), /内容前置校验失败/);
});
