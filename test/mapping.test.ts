import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';
import {
  composeMappings,
  identityMapping,
  invertMapping,
  mapBoundary,
} from '../src/mapping.ts';
import { groundTruthForward } from './helpers.ts';

test('composition matches the per-character identity ground truth', () => {
  const h = new History('abcdef');
  const v1 = h.head;
  const v2 = h.addVersion({ start: 2, deleteCount: 2, insert: 'XY' }); // abXYef
  const v3 = h.addVersion({ start: 0, deleteCount: 1 }); // bXYef
  const v4 = h.addVersion({ start: 4, deleteCount: 0, insert: '!' }); // bXYe!f

  const composed = composeMappings(
    composeMappings(h.mappingBetween(v1, v2), h.mappingBetween(v2, v3)),
    h.mappingBetween(v3, v4),
  );
  const direct = h.mappingBetween(v1, v4);
  assert.deepEqual(composed.forward, direct.forward);
  assert.deepEqual(composed.backward, direct.backward);

  const truth = groundTruthForward(h.getVersion(v1), h.getVersion(v4));
  assert.deepEqual(direct.forward, truth);
});

test('composition is associative', () => {
  const h = new History('one\ntwo\r\nthree');
  const v1 = h.head;
  const v2 = h.addVersion({ start: 3, deleteCount: 2, insert: ' ' });
  const v3 = h.addVersion({ start: 6, deleteCount: 1 });
  const v4 = h.addVersion({ start: 0, deleteCount: 0, insert: '#' });
  const m12 = h.mappingBetween(v1, v2);
  const m23 = h.mappingBetween(v2, v3);
  const m34 = h.mappingBetween(v3, v4);
  const left = composeMappings(composeMappings(m12, m23), m34);
  const right = composeMappings(m12, composeMappings(m23, m34));
  assert.deepEqual(left.forward, right.forward);
  assert.deepEqual(left.backward, right.backward);
});

test('inverse is the structural reverse: survivors round-trip exactly', () => {
  const h = new History('a😀b\r\nc');
  const v1 = h.head;
  const v2 = h.addVersion({ start: 1, deleteCount: 3, insert: 'Q' });
  const m = h.mappingBetween(v1, v2);
  const inv = invertMapping(m);
  assert.equal(inv.from, v2);
  assert.equal(inv.to, v1);
  for (let i = 0; i < m.forward.length; i++) {
    const j = m.forward[i];
    if (j >= 0) assert.equal(inv.forward[j], i, `survivor ${i} round-trips`);
  }
  for (let j = 0; j < m.backward.length; j++) {
    const i = m.backward[j];
    if (i >= 0) assert.equal(m.forward[i], j);
  }
  // forward then backward composed is the identity on surviving chars
  const back = composeMappings(m, inv);
  const id = identityMapping(v1, m.forward.length);
  for (let i = 0; i < m.forward.length; i++) {
    if (m.forward[i] >= 0) assert.equal(back.forward[i], id.forward[i]);
  }
});

test('mapBoundary collapses deleted context onto the deletion site', () => {
  const h = new History('abcde');
  const v1 = h.head;
  const v2 = h.addVersion({ start: 1, deleteCount: 3 }); // "ae"
  const m = h.mappingBetween(v1, v2);
  assert.equal(mapBoundary(m, 2, 'left'), 1);
  assert.equal(mapBoundary(m, 2, 'right'), 1);
  assert.equal(mapBoundary(m, 0, 'right'), 0);
  assert.equal(mapBoundary(m, 5, 'left'), 2);
  // backward: boundary 1 in "ae" maps back next to the deleted "bcd"
  const inv = h.mappingBetween(v2, v1);
  assert.equal(mapBoundary(inv, 1, 'left'), 1);
  assert.equal(mapBoundary(inv, 1, 'right'), 4);
});
