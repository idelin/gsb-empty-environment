import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { solveDecisionTree, formatDecisionTree, MAX_CANDIDATES } from '../dist/solver.js';

const load = (p) => JSON.parse(readFileSync(new URL(p, import.meta.url), 'utf8'));

let passed = 0;
const test = (name, fn) => {
  fn();
  passed++;
  console.log(`ok - ${name}`);
};

// Independent brute force (array-based, no memo/bitmasks) of the minimax recurrence.
function bruteCost(indices, attrs, rows) {
  const sigs = new Set(indices.map((i) => JSON.stringify(rows[i])));
  if (sigs.size === 1) return 0;
  let best = Infinity;
  for (let a = 0; a < attrs.length; a++) {
    const groups = new Map();
    for (const i of indices) {
      const k = JSON.stringify(rows[i][a]);
      if (!groups.has(k)) groups.set(k, []);
      groups.get(k).push(i);
    }
    if (groups.size < 2) continue;
    let worst = 0;
    for (const g of groups.values()) worst = Math.max(worst, bruteCost(g, attrs, rows));
    best = Math.min(best, attrs[a].cost + worst);
  }
  return best;
}

// Worst-case cost of the best FIXED question order (same attribute sequence everywhere).
function bestFixedOrderCost(problem) {
  const { attributes, candidates } = problem;
  const rows = candidates.map((c) => attributes.map((a) => c.values[a.name]));
  const sig = (i) => JSON.stringify(rows[i]);
  const all = candidates.map((_, i) => i);
  const costWithOrder = (order) => {
    const walk = (indices, depth) => {
      if (new Set(indices.map(sig)).size === 1) return 0;
      if (depth >= order.length) return 0;
      const a = order[depth];
      const groups = new Map();
      for (const i of indices) {
        const k = JSON.stringify(rows[i][a]);
        if (!groups.has(k)) groups.set(k, []);
        groups.get(k).push(i);
      }
      let worst = 0;
      for (const g of groups.values()) worst = Math.max(worst, walk(g, depth + 1));
      return attributes[a].cost + worst;
    };
    return walk(all, 0);
  };
  let best = Infinity;
  const perm = attributes.map((_, i) => i);
  const visit = (k) => {
    if (k === perm.length) {
      best = Math.min(best, costWithOrder(perm));
      return;
    }
    for (let i = k; i < perm.length; i++) {
      [perm[k], perm[i]] = [perm[i], perm[k]];
      visit(k + 1);
      [perm[k], perm[i]] = [perm[i], perm[k]];
    }
  };
  visit(0);
  return best;
}

// Simulate the returned tree for every candidate; check leaf membership and path cost.
function checkTreeAgainstProblem(problem, result) {
  const { attributes, candidates } = problem;
  for (const cand of candidates) {
    let node = result.tree;
    let cost = 0;
    while (node.kind !== 'leaf') {
      cost += node.attributeCost;
      const value = cand.values[node.attribute];
      const branch = node.branches.find(
        (b) => JSON.stringify(b.value) === JSON.stringify(value ?? null),
      );
      assert.ok(branch, `no branch for value ${JSON.stringify(value)} of ${node.attribute}`);
      assert.ok(branch.candidates.includes(cand.name), 'branch must list the candidate');
      node = branch.child;
    }
    assert.ok(node.candidates.includes(cand.name));
    assert.ok(cost <= result.worstCaseCost, 'path cost must not exceed worst-case cost');
    // Every leaf-mate must be attribute-identical (equivalence class preserved).
    for (const other of node.candidates) {
      const oc = candidates.find((c) => c.name === other);
      for (const a of attributes) {
        assert.equal(JSON.stringify(oc.values[a.name] ?? null), JSON.stringify(cand.values[a.name] ?? null));
      }
    }
  }
  // Worst-case cost is actually attained by some root-to-leaf path.
  const pathCost = (node) =>
    node.kind === 'leaf' ? 0 : node.attributeCost + Math.max(...node.branches.map((b) => pathCost(b.child)));
  assert.equal(pathCost(result.tree), result.worstCaseCost);
}

test('adaptive tree beats every fixed question order', () => {
  const problem = load('../examples/adaptive.json');
  const result = solveDecisionTree(problem);
  assert.equal(result.worstCaseCost, 2);
  assert.equal(bestFixedOrderCost(problem), 3);
  checkTreeAgainstProblem(problem, result);
  // The two branches below the root ask different attributes (truly adaptive).
  const secondLevel = new Set(result.tree.branches.map((b) => b.child.attribute));
  assert.equal(secondLevel.size, 2);
});

test('indistinguishable candidates stay in one equivalence group', () => {
  const problem = load('../examples/equivalence.json');
  const result = solveDecisionTree(problem);
  assert.equal(result.worstCaseCost, 1);
  assert.deepEqual(result.equivalenceGroups, [['P', 'Q']]);
  const leaf = result.tree.branches.find((b) => b.candidates.includes('P')).child;
  assert.deepEqual(leaf, { kind: 'leaf', candidates: ['P', 'Q'] });
  checkTreeAgainstProblem(problem, result);
});

test('cheap attribute chain beats one expensive all-distinguishing query', () => {
  const problem = load('../examples/costly.json');
  const result = solveDecisionTree(problem);
  assert.equal(result.worstCaseCost, 6); // 2+2+2 < 10
  checkTreeAgainstProblem(problem, result);
});

test('18 candidates with 5 binary attributes need worst-case cost 5', () => {
  const attributes = [0, 1, 2, 3, 4].map((i) => ({ name: `b${i}`, cost: 1 }));
  const candidates = [];
  for (let x = 0; x < 18; x++) {
    const values = {};
    for (let i = 0; i < 5; i++) values[`b${i}`] = (x >> i) & 1;
    candidates.push({ name: `c${x}`, values });
  }
  const problem = { attributes, candidates };
  const result = solveDecisionTree(problem);
  assert.equal(result.worstCaseCost, 5); // ceil(log2 18)
  checkTreeAgainstProblem(problem, result);
});

test('random instances match independent brute force', () => {
  let seed = 12345;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let iter = 0; iter < 300; iter++) {
    const m = 1 + Math.floor(rand() * 3);
    const n = 1 + Math.floor(rand() * 6);
    const alphabet = 1 + Math.floor(rand() * 3);
    const attributes = [];
    for (let a = 0; a < m; a++) {
      attributes.push({ name: `a${a}`, cost: 1 + Math.floor(rand() * 4) });
    }
    const candidates = [];
    for (let j = 0; j < n; j++) {
      const values = {};
      for (const a of attributes) values[a.name] = Math.floor(rand() * alphabet);
      candidates.push({ name: `c${j}`, values });
    }
    const problem = { attributes, candidates };
    const rows = candidates.map((c) => attributes.map((a) => c.values[a.name]));
    const expected = bruteCost(candidates.map((_, i) => i), attributes, rows);
    const result = solveDecisionTree(problem);
    assert.equal(result.worstCaseCost, expected, `iter ${iter}: ${JSON.stringify(problem)}`);
    checkTreeAgainstProblem(problem, result);
  }
});

test('validation rejects bad input', () => {
  const tooMany = {
    attributes: [{ name: 'a', cost: 1 }],
    candidates: Array.from({ length: MAX_CANDIDATES + 1 }, (_, i) => ({
      name: `c${i}`,
      values: { a: i },
    })),
  };
  assert.throws(() => solveDecisionTree(tooMany), /at most 18/i);
  assert.throws(
    () => solveDecisionTree({ attributes: [{ name: 'a', cost: -1 }], candidates: [{ name: 'x', values: { a: 1 } }] }),
    /cost/,
  );
  assert.throws(
    () => solveDecisionTree({ attributes: [{ name: 'a', cost: 1 }], candidates: [{ name: 'x', values: {} }] }),
    /missing a value/,
  );
});

test('formatter prints every branch candidate set', () => {
  const problem = load('../examples/adaptive.json');
  const text = formatDecisionTree(solveDecisionTree(problem));
  assert.match(text, /Worst-case total cost: 2/);
  assert.match(text, /\? color/);
  assert.match(text, /= "red" -> candidates \[A, B\]/);
  assert.match(text, /= "blue" -> candidates \[C, D, E\]/);
  const eq = formatDecisionTree(solveDecisionTree(load('../examples/equivalence.json')));
  assert.match(eq, /equivalence group: cannot be distinguished/);
});

console.log(`\n${passed} tests passed`);
