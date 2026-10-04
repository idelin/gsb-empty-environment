import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildPlan } from '../src/plan.ts';
import { applyPlan } from '../src/apply.ts';
import { SnapshotError } from '../src/snapshot.ts';
import type { PlanOp } from '../src/types.ts';
import { dir, file, snap } from './helpers.ts';

function opIndex(ops: PlanOp[], predicate: (op: PlanOp) => boolean): number {
  const index = ops.findIndex(predicate);
  assert.notEqual(index, -1, 'expected op to exist');
  return index;
}

test('deletes run children-first, creates run parents-first', () => {
  const oldSnap = snap(
    dir('root', null, 'root'),
    dir('d', 'root', 'd'),
    dir('dd', 'd', 'dd'),
    file('f', 'dd', 'f.txt', 'x'),
  );
  const newSnap = snap(dir('root', null, 'root'), dir('n', 'root', 'n'), dir('nn', 'n', 'nn'));

  const plan = buildPlan(oldSnap, newSnap);
  const iF = opIndex(plan.ops, (op) => op.op === 'delete' && op.id === 'f');
  const iDd = opIndex(plan.ops, (op) => op.op === 'delete' && op.id === 'dd');
  const iD = opIndex(plan.ops, (op) => op.op === 'delete' && op.id === 'd');
  assert.ok(iF < iDd && iDd < iD, 'deletes must be children-first');

  const iN = opIndex(plan.ops, (op) => op.op === 'create' && op.node.id === 'n');
  const iNn = opIndex(plan.ops, (op) => op.op === 'create' && op.node.id === 'nn');
  assert.ok(iN < iNn, 'creates must be parents-first');

  applyPlan(oldSnap, plan, newSnap);
});

test('sibling name swap is broken with a temporary move', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('a', 'root', 'a.txt', '1'), file('b', 'root', 'b.txt', '2'));
  const newSnap = snap(dir('root', null, 'root'), file('a', 'root', 'b.txt', '1'), file('b', 'root', 'a.txt', '2'));

  const plan = buildPlan(oldSnap, newSnap);
  const tempOps = plan.ops.filter((op) => op.op === 'moveToTemp');
  assert.equal(tempOps.length, 1, 'exactly one temp move should break the swap cycle');

  const result = applyPlan(oldSnap, plan, newSnap);
  assert.equal(result.nodes.length, 3);
});

test('moving a node out of a deleted directory is ordered before the delete', () => {
  const oldSnap = snap(dir('root', null, 'root'), dir('d', 'root', 'd'), file('f', 'd', 'f.txt', 'x'));
  const newSnap = snap(dir('root', null, 'root'), dir('n', 'root', 'n'), file('f', 'n', 'f.txt', 'x'));

  const plan = buildPlan(oldSnap, newSnap);
  const iCreateN = opIndex(plan.ops, (op) => op.op === 'create' && op.node.id === 'n');
  const iMoveF = opIndex(plan.ops, (op) => op.op === 'move' && op.id === 'f');
  const iDeleteD = opIndex(plan.ops, (op) => op.op === 'delete' && op.id === 'd');
  assert.ok(iCreateN < iMoveF, 'destination dir must exist before the move');
  assert.ok(iMoveF < iDeleteD, 'child must be evacuated before its old parent is deleted');

  applyPlan(oldSnap, plan, newSnap);
});

test('create at a path freed by a delete is ordered after the delete', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('old', 'root', 'config', '1'));
  const newSnap = snap(dir('root', null, 'root'), file('new', 'root', 'config', '2'));

  const plan = buildPlan(oldSnap, newSnap);
  const iDelete = opIndex(plan.ops, (op) => op.op === 'delete' && op.id === 'old');
  const iCreate = opIndex(plan.ops, (op) => op.op === 'create' && op.node.id === 'new');
  assert.ok(iDelete < iCreate);

  applyPlan(oldSnap, plan, newSnap);
});

test('parent generation replacement routes the child through a temp move', () => {
  const oldSnap = snap(dir('p', null, 'p', 1), file('c', 'p', 'c.txt', 'x'));
  const newSnap = snap(dir('p', null, 'p', 2), file('c', 'p', 'c.txt', 'x'));

  const plan = buildPlan(oldSnap, newSnap);
  assert.ok(
    plan.ops.some((op) => op.op === 'moveToTemp' && op.id === 'c'),
    'child must be parked while its parent directory is replaced',
  );
  const iTemp = opIndex(plan.ops, (op) => op.op === 'moveToTemp' && op.id === 'c');
  const iDelete = opIndex(plan.ops, (op) => op.op === 'delete' && op.id === 'p');
  const iCreate = opIndex(plan.ops, (op) => op.op === 'create' && op.node.id === 'p');
  const iMove = opIndex(plan.ops, (op) => op.op === 'move' && op.id === 'c');
  assert.ok(iTemp < iDelete && iDelete < iCreate && iCreate < iMove);

  const result = applyPlan(oldSnap, plan, newSnap);
  const child = result.nodes.find((n) => n.id === 'c');
  assert.equal(child?.parentId, 'p');
});

test('moving a directory carries its children without extra ops', () => {
  const oldSnap = snap(
    dir('root', null, 'root'),
    dir('sub', 'root', 'sub'),
    dir('d', 'root', 'd'),
    file('f', 'd', 'f.txt', 'x'),
    dir('inner', 'd', 'inner'),
    file('g', 'inner', 'g.txt', 'y'),
  );
  const newSnap = snap(
    dir('root', null, 'root'),
    dir('sub', 'root', 'sub'),
    dir('d', 'sub', 'renamed-d'),
    file('f', 'd', 'f.txt', 'x'),
    dir('inner', 'd', 'inner'),
    file('g', 'inner', 'g.txt', 'y'),
  );

  const plan = buildPlan(oldSnap, newSnap);
  assert.deepEqual(plan.ops.map((op) => op.op), ['move']);
  applyPlan(oldSnap, plan, newSnap);
});

test('a move into a deleted parent is rejected as an inconsistent snapshot', () => {
  const oldSnap = snap(dir('root', null, 'root'), dir('d', 'root', 'd'), file('f', 'root', 'f.txt', 'x'));
  const newSnap = snap(dir('root', null, 'root'), file('f', 'd', 'f.txt', 'x'));

  assert.throws(() => buildPlan(oldSnap, newSnap), SnapshotError);
});

test('two nodes ending at the same target path are rejected as invalid', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('a', 'root', 'a.txt', '1'), file('b', 'root', 'b.txt', '2'));
  const newSnap = snap(dir('root', null, 'root'), file('a', 'root', 'b.txt', '1'), file('b', 'root', 'b.txt', '2'));

  assert.throws(() => buildPlan(oldSnap, newSnap), SnapshotError);
});

test('content updates are included in the plan', () => {
  const oldSnap = snap(dir('root', null, 'root'), file('f', 'root', 'f.txt', '1'));
  const newSnap = snap(dir('root', null, 'root'), file('f', 'root', 'f.txt', '2'));

  const plan = buildPlan(oldSnap, newSnap);
  assert.deepEqual(plan.ops, [{ op: 'updateContent', id: 'f', generation: 1, digest: '2' }]);
  applyPlan(oldSnap, plan, newSnap);
});
