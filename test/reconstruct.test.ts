import { test } from 'node:test';
import assert from 'node:assert/strict';
import { GrayImage } from '../src/image.ts';
import { CROSS } from '../src/se.ts';
import { dilate } from '../src/ops.ts';
import { reconstructDilation } from '../src/reconstruct.ts';

function corridor(width: number, height: number, row: number, gapX = -1): GrayImage {
  const mask = new GrayImage(width, height);
  for (let x = 0; x < width; x++) {
    if (x !== gapX) mask.set(x, row, 200);
  }
  return mask;
}

test('reconstruction propagates far beyond the kernel radius and stays under the mask', () => {
  const w = 40;
  const mask = corridor(w, 3, 1);
  const marker = new GrayImage(w, 3);
  marker.set(0, 1, 150);
  const res = reconstructDilation(marker, mask, { connectivity: CROSS });
  assert.ok(res.converged);
  assert.equal(res.out.get(w - 1, 1), 150, 'far end reached (39 px >> radius 1)');
  for (let i = 0; i < res.out.data.length; i++) {
    assert.ok(res.out.data[i] <= mask.data[i], 'never exceeds mask');
    assert.ok(res.out.data[i] >= marker.data[i], 'never below marker');
  }
  // fixed point: out == min(dilate(out), mask)
  const dilated = dilate(res.out, { se: CROSS });
  for (let i = 0; i < res.out.data.length; i++) {
    assert.equal(res.out.data[i], Math.min(dilated.data[i], mask.data[i]));
  }
});

test('a gap blocks propagation exactly', () => {
  const mask = corridor(20, 3, 1, 10);
  const marker = new GrayImage(20, 3);
  marker.set(0, 1, 150);
  const res = reconstructDilation(marker, mask, { connectivity: CROSS });
  assert.ok(res.converged);
  assert.equal(res.out.get(9, 1), 150);
  assert.equal(res.out.get(10, 1), 0);
  assert.equal(res.out.get(11, 1), 0);
});

test('budget exhaustion is reported, partial result not a fixed point', () => {
  const w = 30;
  const mask = corridor(w, 3, 1);
  const marker = new GrayImage(w, 3);
  marker.set(w - 1, 1, 150); // propagation must travel right-to-left: 1 px per sweep
  const res = reconstructDilation(marker, mask, { connectivity: CROSS, budget: 5 });
  assert.equal(res.converged, false);
  assert.equal(res.iterations, 5);
  assert.ok(res.out.get(0, 1) === 0, 'front has not arrived yet');
  assert.ok(res.out.get(w - 6, 1) === 150, 'front advanced ~5 px');
  const full = reconstructDilation(marker, mask, { connectivity: CROSS });
  assert.ok(full.converged);
  assert.equal(full.out.get(0, 1), 150);
});

test('label gate freezes pixels at min(marker, mask)', () => {
  const mask = corridor(12, 3, 1);
  const marker = new GrayImage(12, 3);
  marker.set(0, 1, 150);
  const label = new Uint8Array(12 * 3).fill(1);
  label[1 * 12 + 5] = 0; // freeze (5,1)
  const res = reconstructDilation(marker, mask, { connectivity: CROSS, label });
  assert.ok(res.converged);
  assert.equal(res.out.get(4, 1), 150);
  assert.equal(res.out.get(5, 1), 0, 'frozen pixel stays at min(marker, mask)');
  assert.equal(res.out.get(6, 1), 0, 'frozen pixel blocks the 1-px corridor');
});
