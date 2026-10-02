import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  cloneVV,
  coversVV,
  equalVV,
  getSeq,
  mergeVV,
} from '../src/versionVector.ts';

test('getSeq defaults to 0 for unknown replicas', () => {
  assert.equal(getSeq({}, 'A'), 0);
  assert.equal(getSeq({ A: 3 }, 'A'), 3);
});

test('mergeVV is the component-wise join', () => {
  assert.deepEqual(mergeVV({ A: 2, B: 1 }, { A: 1, C: 5 }), { A: 2, B: 1, C: 5 });
});

test('mergeVV does not mutate its inputs', () => {
  const a = { A: 1 };
  const b = { B: 2 };
  mergeVV(a, b);
  assert.deepEqual(a, { A: 1 });
  assert.deepEqual(b, { B: 2 });
});

test('coversVV / equalVV compare vectors component-wise', () => {
  assert.ok(coversVV({ A: 3, B: 2 }, { A: 3 }));
  assert.ok(!coversVV({ A: 2 }, { A: 3 }));
  assert.ok(equalVV({ A: 1, B: 2 }, { B: 2, A: 1 }));
  assert.ok(!equalVV({ A: 1 }, { A: 1, B: 1 }));
});

test('cloneVV is an independent copy', () => {
  const vv = { A: 1 };
  const copy = cloneVV(vv);
  copy.A = 99;
  assert.equal(vv.A, 1);
});
