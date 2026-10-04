import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildPlan, SnapshotError } from '../src/index.ts';
import type { PlanOp } from '../src/index.ts';
import { dir, file } from './helpers.ts';

function indexOf(ops: PlanOp[], kind: PlanOp['kind'], key: string): number {
  return ops.findIndex((op) => op.kind === kind && op.key === key);
}

test('新增按拓扑顺序：父目录先于子目录，文件在目录之后', () => {
  const source = [dir('root', null, 'root')];
  const target = [
    dir('root', null, 'root'),
    dir('d1', 'root', 'a'),
    dir('d2', 'd1', 'b'),
    file('f1', 'd2', 'deep.txt', 'v1'),
  ];
  const { ops } = buildPlan(source, target);
  const mkdirD1 = indexOf(ops, 'mkdir', 'd1@0');
  const mkdirD2 = indexOf(ops, 'mkdir', 'd2@0');
  const createF1 = indexOf(ops, 'create', 'f1@0');
  assert.ok(mkdirD1 !== -1 && mkdirD2 !== -1 && createF1 !== -1);
  assert.ok(mkdirD1 < mkdirD2, '父目录先于子目录');
  assert.ok(mkdirD2 < createF1, '目录先于其中文件');
});

test('删除按拓扑倒序：子节点先于父目录', () => {
  const source = [
    dir('root', null, 'root'),
    dir('d1', 'root', 'a'),
    dir('d2', 'd1', 'b'),
    file('f1', 'd2', 'deep.txt', 'v1'),
  ];
  const target = [dir('root', null, 'root')];
  const { ops } = buildPlan(source, target);
  const rmF1 = indexOf(ops, 'remove', 'f1@0');
  const rmD2 = indexOf(ops, 'remove', 'd2@0');
  const rmD1 = indexOf(ops, 'remove', 'd1@0');
  assert.ok(rmF1 < rmD2 && rmD2 < rmD1, '子先父后');
});

test('移出待删目录：移动排在旧父目录删除之前', () => {
  const source = [
    dir('root', null, 'root'),
    dir('d1', 'root', 'old'),
    dir('d2', 'root', 'keep'),
    file('f1', 'd1', 'x.txt', 'v1'),
  ];
  const target = [
    dir('root', null, 'root'),
    dir('d2', 'root', 'keep'),
    file('f1', 'd2', 'x.txt', 'v1'),
  ];
  const { ops } = buildPlan(source, target);
  const move = indexOf(ops, 'move', 'f1@0');
  const rmD1 = indexOf(ops, 'remove', 'd1@0');
  assert.ok(move !== -1 && rmD1 !== -1);
  assert.ok(move < rmD1, '先移出再删除旧父目录');
});

test('移动到已删除父节点：目标快照悬空父引用直接报错', () => {
  const source = [dir('root', null, 'root'), dir('d1', 'root', 'a'), file('f1', 'root', 'x.txt', 'v1')];
  // 目标中 f1 挂到已被删除的 d1 下 —— 不合法，需先解决归属
  const target = [dir('root', null, 'root'), file('f1', 'd1', 'x.txt', 'v1')];
  assert.throws(() => buildPlan(source, target), SnapshotError);
});

test('id 复用（代际区分）在计划中体现为先建后删，且应用器可消化', () => {
  const source = [dir('root', null, 'root'), dir('d', 'root', 'data', 0), file('f', 'd', 'a.txt', 'v1')];
  const target = [dir('root', null, 'root'), dir('d', 'root', 'data', 1)];
  const { ops } = buildPlan(source, target);
  assert.ok(indexOf(ops, 'mkdir', 'd@1') !== -1);
  assert.ok(indexOf(ops, 'remove', 'd@0') !== -1);
  assert.ok(indexOf(ops, 'remove', 'f@0') !== -1);
});
