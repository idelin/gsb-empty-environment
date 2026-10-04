import { test } from 'node:test';
import assert from 'node:assert/strict';
import { applyPlan, ApplyError } from '../src/apply.ts';
import { buildPlan } from '../src/plan.ts';
import type { PlanOp } from '../src/types.ts';
import { dir, file, snap } from './helpers.ts';

test('applies a full plan and returns the target tree', () => {
  const oldSnap = snap(
    dir('root', null, 'root'),
    dir('src', 'root', 'src'),
    file('a', 'src', 'a.ts', '1'),
    file('b', 'src', 'b.ts', '2'),
    file('README', 'root', 'README.md', '3'),
    dir('tmp', 'root', 'tmp'),
    file('junk', 'tmp', 'junk.txt', '4'),
  );
  const newSnap = snap(
    dir('root', null, 'root'),
    dir('src', 'root', 'source'),
    file('a', 'src', 'a.ts', '1'),
    file('b', 'root', 'b.ts', '2b'),
    file('README', 'src', 'README.md', '3'),
    file('lock', 'root', 'lock.json', '5'),
  );

  const plan = buildPlan(oldSnap, newSnap);
  const result = applyPlan(oldSnap, plan, newSnap);
  assert.equal(result.nodes.length, newSnap.nodes.length);
});

test('rejects a temp path that collides with an existing path', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('a', 'root', 'a.txt', '1'));
  const ops: PlanOp[] = [
    { op: 'moveToTemp', id: 'a', generation: 1, tempPath: '/root' },
  ];
  assert.throws(
    () => applyPlan(oldSnap, ops, oldSnap),
    (err: unknown) => err instanceof ApplyError && /already occupied/.test(err.message),
  );
});

test('rejects a plan that leaves a node parked at a temp path', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('a', 'root', 'a.txt', '1'));
  const ops: PlanOp[] = [
    { op: 'moveToTemp', id: 'a', generation: 1, tempPath: '/.snapdiff-tmp/1' },
  ];
  assert.throws(
    () => applyPlan(oldSnap, ops, oldSnap),
    (err: unknown) => err instanceof ApplyError && /temporary paths/.test(err.message),
  );
});

test('rejects a create whose target path is occupied', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('a', 'root', 'a.txt', '1'));
  const ops: PlanOp[] = [
    { op: 'create', node: { id: 'b', generation: 1, type: 'file', parentId: 'root', name: 'a.txt', digest: '9' } },
  ];
  assert.throws(
    () => applyPlan(oldSnap, ops, oldSnap),
    (err: unknown) => err instanceof ApplyError && /already occupied/.test(err.message),
  );
});

test('rejects deleting a directory that still has children', () => {
  const oldSnap = snap(dir('root', null, 'root'), dir('d', 'root', 'd'), file('f', 'd', 'f.txt', '1'));
  const ops: PlanOp[] = [{ op: 'delete', id: 'd', generation: 1 }];
  assert.throws(
    () => applyPlan(oldSnap, ops, oldSnap),
    (err: unknown) => err instanceof ApplyError && /still contains/.test(err.message),
  );
});

test('rejects ops referencing missing nodes', () => {
  const oldSnap = snap(dir('root', null, 'root'));
  const ops: PlanOp[] = [{ op: 'move', id: 'ghost', generation: 1, toParentId: null, toName: 'x' }];
  assert.throws(
    () => applyPlan(oldSnap, ops, oldSnap),
    (err: unknown) => err instanceof ApplyError && /does not exist/.test(err.message),
  );
});

test('verifies the final tree against the target snapshot', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('a', 'root', 'a.txt', '1'));
  const expected = snap(dir('root', null, 'root'), file('a', 'root', 'renamed.txt', '1'));
  const ops: PlanOp[] = [];
  assert.throws(
    () => applyPlan(oldSnap, ops, expected),
    (err: unknown) => err instanceof ApplyError && /does not match the target/.test(err.message),
  );
});

test('accepts a plan given as a bare op array or a plan object', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('a', 'root', 'a.txt', '1'));
  const newSnap = snap(dir('root', null, 'root'), file('a', 'root', 'b.txt', '1'));
  const plan = buildPlan(oldSnap, newSnap);
  applyPlan(oldSnap, plan, newSnap);
  applyPlan(oldSnap, plan.ops, newSnap);
});
