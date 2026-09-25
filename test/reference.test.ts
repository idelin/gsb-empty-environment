import { test } from 'node:test';
import assert from 'node:assert/strict';
import { HeaderStore, type Op } from '../src/ops.ts';
import { rebuildFromLeafOrder, diffSnapshots, checkInvariants } from '../src/reference.ts';
import { glyphMetrics, sampleDoc } from '../src/fixtures.ts';
import type { HeaderSnapshot } from '../src/types.ts';

function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const LEAVES = ['c-name', 'c-age', 'c-math', 'c-eng', 'c-rank', 'c-note'];
const GROUPS = ['g-basic', 'g-metrics', 'g-scores'];
const MOVE_TARGETS = ['root', 'g-basic', 'g-metrics', 'g-scores'];

function randomOp(rand: () => number): Op {
  const pick = <T>(items: T[]): T => items[Math.floor(rand() * items.length)];
  const roll = rand();
  if (roll < 0.3) {
    return { type: rand() < 0.5 ? 'hide' : 'show', leafId: pick(LEAVES) };
  }
  if (roll < 0.55) {
    return { type: 'setCollapsed', groupId: pick(GROUPS), collapsed: rand() < 0.5 };
  }
  return {
    type: 'move',
    leafId: pick(LEAVES),
    targetGroupId: pick(MOVE_TARGETS),
    index: Math.floor(rand() * 4),
  };
}

function published(snapshot: HeaderSnapshot): Omit<HeaderSnapshot, 'version'> {
  const { version, ...rest } = snapshot;
  return rest;
}

test('reference rebuild from leaf order matches any random edit sequence', () => {
  const rand = mulberry32(20260926);
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const initial = store.snapshot;
  const transactionCount = 300;

  for (let t = 0; t < transactionCount; t++) {
    const ops: Op[] = [];
    const opCount = 1 + Math.floor(rand() * 4);
    for (let i = 0; i < opCount; i++) ops.push(randomOp(rand));
    store.commit(ops);

    const reference = rebuildFromLeafOrder(store.doc, glyphMetrics, store.version);
    assert.deepEqual(
      diffSnapshots(store.snapshot, reference),
      [],
      `snapshot diverged from reference after transaction ${t}: ${JSON.stringify(ops)}`,
    );
    assert.deepEqual(
      checkInvariants(store.snapshot),
      [],
      `invariants broken after transaction ${t}: ${JSON.stringify(ops)}`,
    );
  }

  for (let t = 0; t < transactionCount; t++) store.undo();
  assert.deepEqual(
    published(store.snapshot),
    published(initial),
    'undoing every transaction must restore the initial published state',
  );
});

test('empty groups never leak invalid spans across targeted sequences', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const sequences: Op[][] = [
    [
      { type: 'hide', leafId: 'c-math' },
      { type: 'hide', leafId: 'c-eng' },
    ],
    [
      { type: 'move', leafId: 'c-math', targetGroupId: 'root', index: 0 },
      { type: 'move', leafId: 'c-eng', targetGroupId: 'root', index: 1 },
      { type: 'move', leafId: 'c-rank', targetGroupId: 'g-basic', index: 0 },
    ],
    [
      { type: 'setCollapsed', groupId: 'g-scores', collapsed: true },
      { type: 'hide', leafId: 'c-rank' },
    ],
  ];
  for (const ops of sequences) {
    store.commit(ops);
    const reference = rebuildFromLeafOrder(store.doc, glyphMetrics, store.version);
    assert.deepEqual(diffSnapshots(store.snapshot, reference), []);
    assert.deepEqual(checkInvariants(store.snapshot), []);
    for (const cell of store.snapshot.cells) {
      assert.ok(cell.rowspan >= 1 && cell.colspan >= 1, `invalid span on ${cell.key}`);
    }
  }
});
