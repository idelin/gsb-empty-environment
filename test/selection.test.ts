import { test } from 'node:test';
import assert from 'node:assert/strict';
import { HeaderStore } from '../src/ops.ts';
import { mapSelection } from '../src/selection.ts';
import { glyphMetrics, sampleDoc } from '../src/fixtures.ts';

test('unchanged header maps a region to itself', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const v0 = store.snapshot;
  const v1 = store.commit([{ type: 'show', leafId: 'c-name' }]); // no-op op
  assert.deepEqual(mapSelection(v0, v1, { start: 1, end: 3 }), {
    status: 'mapped',
    candidates: [{ start: 1, end: 3 }],
  });
});

test('a reorder that keeps the selection contiguous yields one candidate', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const v0 = store.snapshot;
  const v1 = store.commit([{ type: 'move', leafId: 'c-name', targetGroupId: 'g-basic', index: 2 }]);
  // order inside g-basic is now c-age, c-name; the {c-name,c-age} selection is still contiguous
  assert.deepEqual(mapSelection(v0, v1, { start: 0, end: 1 }), {
    status: 'mapped',
    candidates: [{ start: 0, end: 1 }],
  });
});

test('a reorder that splits the selection yields multiple candidates', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const v0 = store.snapshot;
  const v1 = store.commit([{ type: 'move', leafId: 'c-age', targetGroupId: 'root', index: 3 }]);
  // c-age moved to the end; {c-name, c-age} is now split
  assert.deepEqual(mapSelection(v0, v1, { start: 0, end: 1 }), {
    status: 'mapped',
    candidates: [
      { start: 0, end: 0 },
      { start: 5, end: 5 },
    ],
  });
});

test('a selection over a hidden column is invalidated with the missing keys', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const v0 = store.snapshot;
  const v1 = store.commit([{ type: 'hide', leafId: 'c-age' }]);
  assert.deepEqual(mapSelection(v0, v1, { start: 0, end: 2 }), {
    status: 'invalidated',
    missingKeys: ['leaf:c-age'],
  });
});

test('collapsing a group invalidates selections over its leaves', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const v0 = store.snapshot;
  const v1 = store.commit([{ type: 'setCollapsed', groupId: 'g-scores', collapsed: true }]);
  assert.deepEqual(mapSelection(v0, v1, { start: 2, end: 3 }), {
    status: 'invalidated',
    missingKeys: ['leaf:c-math', 'leaf:c-eng'],
  });
});

test('a selection on a collapsed group column maps while collapsed, invalidates after expand', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  store.commit([{ type: 'setCollapsed', groupId: 'g-metrics', collapsed: true }]);
  const collapsed = store.snapshot;
  const stillCollapsed = store.commit([{ type: 'hide', leafId: 'c-note' }]);
  assert.deepEqual(mapSelection(collapsed, stillCollapsed, { start: 2, end: 2 }), {
    status: 'mapped',
    candidates: [{ start: 2, end: 2 }],
  });
  const expanded = store.commit([{ type: 'setCollapsed', groupId: 'g-metrics', collapsed: false }]);
  assert.deepEqual(mapSelection(collapsed, expanded, { start: 2, end: 2 }), {
    status: 'invalidated',
    missingKeys: ['group:g-metrics'],
  });
});

test('out-of-range regions are rejected', () => {
  const store = new HeaderStore(sampleDoc(), glyphMetrics);
  const v0 = store.snapshot;
  assert.throws(() => mapSelection(v0, v0, { start: 0, end: 6 }), /out of range/);
});
