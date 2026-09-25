import test from 'node:test';
import assert from 'node:assert/strict';
import {
  HeaderStore,
  referenceVisibleColumns,
  type HeaderOp,
} from '../src/index.ts';
import { fixtureState } from '../fixtures/header.fixture.ts';
import {
  assertSnapshotInvariants,
  mulberry32,
  randomOp,
} from './helpers.ts';

// For arbitrary edit sequences (commits, undos, redos) the store's
// recursively derived snapshot must match the independent flat reference
// rebuilt from scratch out of the leaf order.
test('random edit sequences match the independent reference', () => {
  for (let seed = 1; seed <= 25; seed++) {
    const rand = mulberry32(seed);
    const initial = fixtureState();
    const store = new HeaderStore(initial);
    const batches: HeaderOp[][] = [];
    let cursor = 0;

    const check = (): void => {
      const snap = store.snapshot;
      assertSnapshotInvariants(snap);
      const expected = referenceVisibleColumns(initial, batches, cursor);
      assert.deepEqual(
        snap.visibleColumns.map((vc) => ({
          columnId: vc.columnId,
          ownerGroupId: vc.ownerGroupId,
        })),
        expected,
        `seed ${seed} cursor ${cursor}`,
      );
    };

    for (let step = 0; step < 120; step++) {
      const roll = rand();
      if (roll < 0.7 || cursor === 0) {
        const size = 1 + Math.floor(rand() * 3);
        const ops: HeaderOp[] = [];
        for (let i = 0; i < size; i++) {
          ops.push(randomOp(rand, store.stateAt(cursor)!));
        }
        batches.length = cursor;
        batches.push(ops);
        store.commit(ops);
        cursor += 1;
      } else if (roll < 0.85) {
        if (store.undo()) cursor -= 1;
      } else {
        if (store.redo()) cursor += 1;
      }
      check();
    }
  }
});

test('reference agrees on a scripted edge-case sequence', () => {
  const initial = fixtureState();
  const store = new HeaderStore(initial);
  const batches: HeaderOp[][] = [
    // Empty the nested group, then move a foreign column into it.
    [
      { type: 'hide', columnIds: ['n1', 'n2'] },
      { type: 'moveColumn', columnId: 'solo', toGroupId: 'nested' },
    ],
    // Collapse a group, then move one of its covered columns elsewhere.
    [
      { type: 'collapse', groupId: 'metrics' },
      { type: 'moveColumn', columnId: 'm1', toGroupId: 'basic' },
    ],
    // Re-show leaves into a collapsed group.
    [{ type: 'show', columnIds: ['n1'] }],
  ];
  for (const ops of batches) store.commit(ops);

  for (let upto = 0; upto <= batches.length; upto++) {
    const expected = referenceVisibleColumns(initial, batches, upto);
    const snap = store.snapshotAt(upto)!;
    assertSnapshotInvariants(snap);
    assert.deepEqual(
      snap.visibleColumns.map((vc) => ({
        columnId: vc.columnId,
        ownerGroupId: vc.ownerGroupId,
      })),
      expected,
      `upto ${upto}`,
    );
  }

  // Spot-check the final state: collapsed metrics owns m2+n1+solo via m2
  // (solo was moved into "nested" by the first batch, n2 stays hidden).
  const final = store.snapshot;
  const collapsed = final.visibleColumns.find(
    (vc) => vc.collapsedGroupId === 'metrics',
  )!;
  assert.equal(collapsed.columnId, 'm2');
  assert.deepEqual(collapsed.coveredColumnIds, ['m2', 'n1', 'solo']);
});
