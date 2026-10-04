import assert from 'node:assert/strict';
import { test } from './harness.mjs';
import { solveAllocation } from '../dist/allocator.js';
import { greedyAllocate } from '../dist/greedy.js';
import { assertFeasible } from './util.mjs';

const costsXY = { X: { Y: 1 }, Y: { X: 1 } };

test('greedy nearest admits less demand than optimal', () => {
  const input = {
    requests: [
      { id: 'r1', region: 'X', demand: 3 },
      { id: 'r2', region: 'X', demand: 3 },
      { id: 'r3', region: 'Y', demand: 5 },
    ],
    nodes: [
      { id: 'A', region: 'X', capacity: 5 },
      { id: 'B', region: 'Y', capacity: 5 },
    ],
    costs: costsXY,
  };
  const greedy = greedyAllocate(input);
  const optimal = solveAllocation(input);
  assert.equal(greedy.stats.admittedDemand, 6);
  assert.equal(optimal.stats.admittedDemand, 8);
  assert.ok(optimal.stats.admittedDemand > greedy.stats.admittedDemand);
  assert.equal(optimal.rejected.length, 1);
  assert.equal(optimal.assignments.r3, 'B');
  assertFeasible(optimal, input);
});

test('single demand larger than every capacity is rejected', () => {
  const input = {
    requests: [
      { id: 'big', region: 'X', demand: 100 },
      { id: 'small', region: 'X', demand: 5 },
    ],
    nodes: [
      { id: 'A', region: 'X', capacity: 10 },
      { id: 'B', region: 'Y', capacity: 10 },
    ],
    costs: costsXY,
  };
  const result = solveAllocation(input);
  assert.deepEqual(result.rejected, ['big']);
  assert.equal(result.assignments.small, 'A');
  assert.equal(result.stats.admittedDemand, 5);
  const ledgerA = result.capacityLedger.find((e) => e.nodeId === 'A');
  assert.deepEqual(
    { used: ledgerA.used, remaining: ledgerA.remaining },
    { used: 5, remaining: 5 },
  );
  assertFeasible(result, input);
});

test('cross-region cost tie breaks by node-id vector', () => {
  const input = {
    requests: [{ id: 'r1', region: 'Y', demand: 4 }],
    nodes: [
      { id: 'nB', region: 'X', capacity: 10 },
      { id: 'nC', region: 'Z', capacity: 10 },
    ],
    costs: { Y: { X: 2, Z: 2 }, X: { Y: 1, Z: 1 }, Z: { X: 1, Y: 1 } },
  };
  const result = solveAllocation(input);
  assert.equal(result.stats.crossRegionCost, 2);
  assert.equal(result.assignments.r1, 'nB');
  assertFeasible(result, input);
});

test('node exit triggers recompute with minimal migrations', () => {
  const input = {
    requests: [
      { id: 'r1', region: 'X', demand: 2 },
      { id: 'r2', region: 'X', demand: 2 },
    ],
    nodes: [
      { id: 'A', region: 'X', capacity: 10 },
      { id: 'B', region: 'X', capacity: 10 },
    ],
    costs: costsXY,
    previous: { r1: 'retired-node', r2: 'A' },
  };
  const result = solveAllocation(input);
  assert.equal(result.assignments.r2, 'A');
  assert.equal(result.assignments.r1, 'A');
  assert.deepEqual(result.migrations, [
    { requestId: 'r1', from: 'retired-node', to: 'A' },
  ]);
  assert.equal(result.stats.migratedCount, 1);
  assertFeasible(result, input);
});

test('capacity shrink recomputes batch without overcommit', () => {
  const input = {
    requests: [
      { id: 'r1', region: 'X', demand: 2 },
      { id: 'r2', region: 'X', demand: 2 },
    ],
    nodes: [
      { id: 'A', region: 'X', capacity: 3 },
      { id: 'B', region: 'X', capacity: 10 },
    ],
    costs: costsXY,
    previous: { r1: 'A', r2: 'A' },
  };
  const result = solveAllocation(input);
  assert.equal(result.assignments.r1, 'A');
  assert.equal(result.assignments.r2, 'B');
  assert.deepEqual(result.migrations, [
    { requestId: 'r2', from: 'A', to: 'B' },
  ]);
  const ledgerA = result.capacityLedger.find((e) => e.nodeId === 'A');
  assert.ok(ledgerA.used <= 3);
  assertFeasible(result, input);
});

test('exhausted state budget returns feasible plan flagged not proven', () => {
  const requests = [];
  for (let k = 0; k < 12; k++) {
    requests.push({ id: `r${k}`, region: k % 2 === 0 ? 'X' : 'Y', demand: 1 });
  }
  const input = {
    requests,
    nodes: [
      { id: 'A', region: 'X', capacity: 6 },
      { id: 'B', region: 'Y', capacity: 6 },
      { id: 'C', region: 'X', capacity: 3 },
      { id: 'D', region: 'Y', capacity: 3 },
    ],
    costs: costsXY,
  };
  const result = solveAllocation(input, { maxStates: 1 });
  assert.equal(result.provenOptimal, false);
  assertFeasible(result, input);
  const proven = solveAllocation(input);
  assert.equal(proven.provenOptimal, true);
  assertFeasible(proven, input);
});

test('exhausted time budget also returns feasible plan', () => {
  const requests = [];
  for (let k = 0; k < 14; k++) {
    requests.push({ id: `r${k}`, region: k % 2 === 0 ? 'X' : 'Y', demand: 1 });
  }
  const nodes = [];
  for (let k = 0; k < 6; k++) {
    nodes.push({ id: `n${k}`, region: k % 2 === 0 ? 'X' : 'Y', capacity: 3 });
  }
  const input = { requests, nodes, costs: costsXY };
  const result = solveAllocation(input, { maxStates: 100000000, timeMs: 1 });
  assert.equal(result.provenOptimal, false);
  assertFeasible(result, input);
});

test('solving the same input twice is deterministic', () => {
  const input = {
    requests: [
      { id: 'r1', region: 'X', demand: 3 },
      { id: 'r2', region: 'Y', demand: 4 },
      { id: 'r3', region: 'X', demand: 1 },
    ],
    nodes: [
      { id: 'A', region: 'X', capacity: 4 },
      { id: 'B', region: 'Y', capacity: 4 },
    ],
    costs: costsXY,
    previous: { r1: 'A', r2: 'A' },
  };
  const first = solveAllocation(input);
  const second = solveAllocation(input);
  assert.equal(JSON.stringify(first), JSON.stringify(second));
});
