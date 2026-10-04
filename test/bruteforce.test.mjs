import assert from 'node:assert/strict';
import { test } from './harness.mjs';
import { solveAllocation } from '../dist/allocator.js';
import { bruteForceAllocate } from '../dist/bruteforce.js';
import { makeInstance, mulberry32, projection, shuffled, assertFeasible } from './util.mjs';

test('solver matches exhaustive enumeration on small instances', () => {
  const rand = mulberry32(20261005);
  for (let k = 0; k < 120; k++) {
    const input = makeInstance(rand, { maxRequests: 8, maxNodes: 3 });
    const solved = solveAllocation(input);
    const brute = bruteForceAllocate(input);
    assert.equal(solved.provenOptimal, true);
    assert.equal(
      projection(solved),
      projection(brute),
      `mismatch on instance ${k}: ${JSON.stringify(input)}`,
    );
    assertFeasible(solved, input);
  }
});

test('solver matches exhaustive enumeration on 10-request instances', () => {
  const rand = mulberry32(777);
  for (let k = 0; k < 6; k++) {
    const input = makeInstance(rand, { maxRequests: 10, maxNodes: 3 });
    input.requests = [];
    const count = 10;
    for (let r = 0; r < count; r++) {
      input.requests.push({
        id: `q${r}`,
        region: input.nodes[r % input.nodes.length].region,
        demand: 1 + ((r * 7 + k) % 9),
      });
    }
    input.previous = {};
    for (let r = 0; r < count; r += 3) {
      input.previous[`q${r}`] = r % 2 === 0 ? input.nodes[0].id : 'ghost-1';
    }
    const solved = solveAllocation(input);
    const brute = bruteForceAllocate(input);
    assert.equal(
      projection(solved),
      projection(brute),
      `mismatch on 10-request instance ${k}`,
    );
    assertFeasible(solved, input);
  }
});

test('input permutations yield identical results', () => {
  const rand = mulberry32(424242);
  for (let k = 0; k < 40; k++) {
    const input = makeInstance(rand, { maxRequests: 8, maxNodes: 3 });
    const base = solveAllocation(input);
    for (let trial = 0; trial < 3; trial++) {
      const permuted = {
        requests: shuffled(input.requests, rand),
        nodes: shuffled(input.nodes, rand),
        costs: input.costs,
        previous: input.previous,
      };
      const result = solveAllocation(permuted);
      assert.equal(
        projection(result),
        projection(base),
        `permutation changed result on instance ${k}`,
      );
    }
  }
});
