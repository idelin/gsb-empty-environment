import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';
import { mapBoundary } from '../src/mapping.ts';
import { groundTruthForward } from './helpers.ts';

function buildHistory(): { h: History; vs: number[] } {
  const h = new History('hello😀 world\r\nsecond line');
  const vs = [h.head];
  vs.push(h.addVersion({ start: 0, deleteCount: 0, insert: '>>' }));
  vs.push(h.addVersion({ start: 7, deleteCount: 3, insert: '😀' }));
  vs.push(h.addVersion({ start: 5, deleteCount: 1 }));
  vs.push(h.addVersion({ start: 12, deleteCount: 2, insert: '\n' }));
  return { h, vs };
}

test('compression reclaims unpinned versions but preserves surviving queries', () => {
  const { h, vs } = buildHistory();
  h.pin(vs[2]);

  // capture boundary mappings for all surviving pairs before compression
  const survivors = [vs[0], vs[2], vs[4]];
  const before = new Map<string, number[]>();
  for (const a of survivors) {
    for (const b of survivors) {
      const m = h.mappingBetween(a, b);
      const len = h.getVersion(a).chars.length;
      const mapped: number[] = [];
      for (let boundary = 0; boundary <= len; boundary++) {
        mapped.push(mapBoundary(m, boundary, 'left'), mapBoundary(m, boundary, 'right'));
      }
      before.set(`${a}->${b}`, mapped);
    }
  }

  const reclaimed = h.compress();
  assert.deepEqual(reclaimed.sort(), [vs[1], vs[3]].sort());
  assert.deepEqual(h.liveVersionIds, survivors);

  for (const a of survivors) {
    for (const b of survivors) {
      const m = h.mappingBetween(a, b);
      const len = h.getVersion(a).chars.length;
      const mapped: number[] = [];
      for (let boundary = 0; boundary <= len; boundary++) {
        mapped.push(mapBoundary(m, boundary, 'left'), mapBoundary(m, boundary, 'right'));
      }
      assert.deepEqual(mapped, before.get(`${a}->${b}`), `query ${a}->${b} unchanged`);
      // and it still matches the identity ground truth
      assert.deepEqual(m.forward, groundTruthForward(h.getVersion(a), h.getVersion(b)));
    }
  }

  assert.throws(() => h.getVersion(vs[1]), /reclaimed/);
  assert.throws(() => h.mappingBetween(vs[1], vs[2]), /reclaimed/);
});

test('undo/redo walk the chain; a new edit after undo truncates the redo tail', () => {
  const h = new History('abc');
  const v1 = h.head;
  const v2 = h.addVersion({ start: 3, deleteCount: 0, insert: 'd' });
  const v3 = h.addVersion({ start: 0, deleteCount: 1 });
  assert.equal(h.textOf(v3), 'bcd');

  assert.equal(h.undo(), v2);
  assert.equal(h.textOf(h.head), 'abcd');
  assert.equal(h.undo(), v1);
  assert.equal(h.redo(), v2);

  const v4 = h.addVersion({ start: 0, deleteCount: 0, insert: 'X' });
  assert.equal(h.textOf(v4), 'Xabcd');
  assert.deepEqual(h.liveVersionIds, [v1, v2, v4], 'redo tail truncated');
  assert.throws(() => h.redo(), /nothing to redo/);
});

test('compressed history still supports undo between surviving versions', () => {
  const h = new History('abcdef');
  const v1 = h.head;
  h.addVersion({ start: 1, deleteCount: 1 });
  const v3 = h.addVersion({ start: 1, deleteCount: 1 });
  h.addVersion({ start: 1, deleteCount: 1 });
  const v5 = h.head;
  h.pin(v3);
  h.compress();
  assert.deepEqual(h.liveVersionIds, [v1, v3, v5]);
  assert.equal(h.undo(), v3);
  assert.equal(h.undo(), v1);
  assert.equal(h.textOf(h.head), 'abcdef');
  assert.equal(h.redo(), v3);
  assert.equal(h.textOf(h.head), 'adef');
});
