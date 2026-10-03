import { test } from 'node:test';
import assert from 'node:assert/strict';
import { vec3 } from '../src/vec3.ts';
import { makeCapsule } from '../src/capsule.ts';
import { SpacetimeGrid } from '../src/spacetimeGrid.ts';

test('neighbors in the same cell and slot are found', () => {
  const grid = new SpacetimeGrid(100, 1);
  const capA = makeCapsule(vec3(10, 10, 10), vec3(20, 10, 10), 5);
  const capB = makeCapsule(vec3(30, 10, 10), vec3(40, 10, 10), 5);
  grid.insert(0, 1, capA);
  grid.insert(0, 2, capB);
  assert.deepEqual(grid.queryNeighbors(0, 1, capA), [2]);
  assert.deepEqual(grid.queryNeighbors(0, 2, capB), [1]);
});

test('distant entities are not neighbors', () => {
  const grid = new SpacetimeGrid(100, 1);
  const capA = makeCapsule(vec3(0, 0, 0), vec3(10, 0, 0), 5);
  const capB = makeCapsule(vec3(5000, 0, 0), vec3(5010, 0, 0), 5);
  grid.insert(0, 1, capA);
  grid.insert(0, 2, capB);
  assert.deepEqual(grid.queryNeighbors(0, 1, capA), []);
});

test('time slots isolate identical trajectories', () => {
  const grid = new SpacetimeGrid(100, 1);
  const capA = makeCapsule(vec3(0, 0, 0), vec3(10, 0, 0), 5);
  const capB = makeCapsule(vec3(0, 0, 0), vec3(10, 0, 0), 5);
  grid.insert(3, 1, capA);
  grid.insert(7, 2, capB);
  assert.deepEqual(grid.queryNeighbors(3, 1, capA), []);
  assert.deepEqual(grid.queryNeighbors(7, 1, capA), [2]);
});

test('swept capsule hashes into every overlapped cell', () => {
  const grid = new SpacetimeGrid(100, 1);
  // Sweeps 250 m along x: covers cells 0, 1, 2 (plus radius margin).
  const swept = makeCapsule(vec3(10, 10, 10), vec3(260, 10, 10), 5);
  const far = makeCapsule(vec3(250, 10, 10), vec3(250, 10, 10), 5);
  grid.insert(0, 1, swept);
  grid.insert(0, 2, far);
  assert.deepEqual(grid.queryNeighbors(0, 1, swept), [2]);
});

test('querying entity is excluded from its own neighbor set', () => {
  const grid = new SpacetimeGrid(100, 1);
  const cap = makeCapsule(vec3(0, 0, 0), vec3(10, 0, 0), 5);
  grid.insert(0, 42, cap);
  assert.deepEqual(grid.queryNeighbors(0, 42, cap), []);
});
