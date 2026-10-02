import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pack, verifyAll } from '../src/index.ts';
import type { ItemSpec } from '../src/index.ts';

test('small instance gets an optimality certificate', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 10, height: 10 },
    { id: 'b', width: 10, height: 10 },
    { id: 'c', width: 10, height: 10 },
    { id: 'd', width: 10, height: 10 },
  ];
  // 16x16 page, spacing 0: two 10x10 can never share a page -> 4 pages optimal.
  const r = pack({ pageWidth: 16, pageHeight: 16, spacing: 0, items });
  assert.deepEqual(r.proof, { status: 'optimal', pages: 4 });
  assert.equal(r.pages, 4);
  const v = verifyAll(16, 16, 0, items, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});

test('exact search finds the true minimum page count', () => {
  // Four 8x8 in a 16x16 page with spacing 0 all fit on one page.
  const items: ItemSpec[] = Array.from({ length: 4 }, (_, i) => ({
    id: `q${i}`, width: 8, height: 8,
  }));
  const r = pack({ pageWidth: 16, pageHeight: 16, spacing: 0, items });
  assert.deepEqual(r.proof, { status: 'optimal', pages: 1 });
  assert.equal(r.pages, 1);
});

test('exact search respects fixed obstacles', () => {
  const items: ItemSpec[] = [
    { id: 'pin', width: 16, height: 8, fixed: { page: 0, x: 0, y: 0 } },
    { id: 'a', width: 8, height: 8 },
    { id: 'b', width: 8, height: 8 },
  ];
  const r = pack({ pageWidth: 16, pageHeight: 16, spacing: 0, items });
  assert.equal(r.proof.status, 'optimal');
  assert.equal(r.pages, 1);
  const v = verifyAll(16, 16, 0, items, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});

test('large instance returns bounds and the heuristic label', () => {
  const items: ItemSpec[] = Array.from({ length: 30 }, (_, i) => ({
    id: `s${i}`,
    width: 6 + (i % 5),
    height: 6 + ((i * 2) % 5),
  }));
  const r = pack({ pageWidth: 24, pageHeight: 24, spacing: 1, items });
  assert.equal(r.proof.status, 'heuristic');
  if (r.proof.status === 'heuristic') {
    assert.ok(r.proof.lowerBound >= 1);
    assert.ok(r.proof.upperBound >= r.proof.lowerBound);
    assert.equal(r.proof.upperBound, r.pages);
  }
  const v = verifyAll(24, 24, 1, items, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});

test('node budget exhaustion falls back to the heuristic label', () => {
  const items: ItemSpec[] = Array.from({ length: 8 }, (_, i) => ({
    id: `s${i}`, width: 5 + (i % 3), height: 5 + ((i * 2) % 3),
  }));
  const r = pack(
    { pageWidth: 16, pageHeight: 16, spacing: 1, items },
    { exactNodeLimit: 1 },
  );
  assert.equal(r.proof.status, 'heuristic');
  const v = verifyAll(16, 16, 1, items, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});
