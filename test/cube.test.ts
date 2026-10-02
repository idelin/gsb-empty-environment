import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SpatioTemporalCube } from '../src/cube.ts';

test('accumulates weights per hour layer', () => {
  const cube = new SpatioTemporalCube(4, 8);
  cube.add(0, 100, 1);
  cube.add(0, 100, 2.5);
  cube.add(1, 100, 3);
  cube.add(0, 101, 10);

  const out = new Float64Array(4);
  const matched = cube.windowSums(100, 101, out);
  assert.equal(matched, 1);
  assert.deepEqual([...out], [3.5, 3, 0, 0]);

  cube.windowSums(100, 102, out);
  assert.deepEqual([...out], [13.5, 3, 0, 0]);

  cube.windowSums(200, 201, out);
  assert.deepEqual([...out], [0, 0, 0, 0]);
});

test('memory is bounded: oldest hour evicted beyond capacity', () => {
  const cube = new SpatioTemporalCube(2, 3);
  cube.add(0, 10, 1);
  cube.add(0, 11, 2);
  cube.add(0, 12, 4);
  assert.equal(cube.hoursStored, 3);
  assert.equal(cube.evictions, 0);

  cube.add(0, 13, 8); // evicts hour 10
  assert.equal(cube.hoursStored, 3);
  assert.equal(cube.evictions, 1);
  assert.equal(cube.minHour, 11);

  const out = new Float64Array(2);
  cube.windowSums(10, 14, out);
  assert.deepEqual([...out], [14, 0]); // 2+4+8, hour 10 gone

  cube.add(1, 14, 16); // evicts hour 11
  cube.windowSums(10, 15, out);
  assert.deepEqual([...out], [12, 16]);
});

test('out-of-order hours within capacity are retained', () => {
  const cube = new SpatioTemporalCube(2, 4);
  cube.add(0, 50, 1);
  cube.add(0, 40, 2);
  cube.add(0, 60, 4);
  assert.equal(cube.minHour, 40);
  const out = new Float64Array(2);
  cube.windowSums(0, 100, out);
  assert.deepEqual([...out], [7, 0]);
});

test('memoryBytes reflects the fixed bound', () => {
  const cube = new SpatioTemporalCube(1000, 24);
  assert.equal(cube.memoryBytes, 1000 * 24 * 8);
});
