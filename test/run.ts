import assert from 'node:assert';
import { diff } from '../src/diff.ts';
import { applyPatch, verifyPatch, PatchError } from '../src/apply.ts';
import { checkDeterminism, exhaustiveMinCost } from '../src/exhaustive.ts';
import { countNodes } from '../src/tree.ts';
import type { TreeNode, Op } from '../src/types.ts';
import { N, prng, randomTree, allTrees } from './helpers.ts';

let passed = 0;
let failed = 0;
const failures: string[] = [];

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
  } catch (err) {
    failed++;
    failures.push(`${name}: ${(err as Error).message}`);
  }
}

const opsOf = (patch: Op[], kind: Op['op']): Op[] => patch.filter((o) => o.op === kind);

// ---------------------------------------------------------------- unit cases

test('identical trees -> empty patch, optimal', () => {
  const a = N('a', {}, [N('b', { value: '1' }), N('c')]);
  const r = diff(a, N('a', {}, [N('b', { value: '1' }), N('c')]));
  assert.deepStrictEqual(r.patch, []);
  assert.strictEqual(r.cost, 0);
  assert.strictEqual(r.optimal, true);
});

test('single value update', () => {
  const s = N('a', {}, [N('b', { value: '1' })]);
  const t = N('a', {}, [N('b', { value: '2' })]);
  const r = diff(s, t);
  assert.strictEqual(r.cost, 1);
  assert.strictEqual(r.optimal, true);
  assert.deepStrictEqual(r.patch, [
    { op: 'update', path: [0], type: 'b', value: '2', id: undefined },
  ]);
  assert.ok(verifyPatch(s, r.patch, t));
});

test('insert subtree', () => {
  const s = N('a');
  const t = N('a', {}, [N('b', {}, [N('c')])]);
  const r = diff(s, t);
  assert.strictEqual(r.cost, 2);
  assert.strictEqual(r.optimal, true);
  assert.ok(verifyPatch(s, r.patch, t));
});

test('delete subtree', () => {
  const s = N('a', {}, [N('b', {}, [N('c')]), N('d')]);
  const t = N('a', {}, [N('d')]);
  const r = diff(s, t);
  assert.strictEqual(r.cost, 2);
  assert.strictEqual(r.optimal, true);
  assert.ok(verifyPatch(s, r.patch, t));
});

test('sibling reorder uses moves, not delete+insert', () => {
  const s = N('a', {}, [N('x'), N('y'), N('z')]);
  const t = N('a', {}, [N('z'), N('x'), N('y')]);
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  assert.strictEqual(r.optimal, true);
  // Moving z to the front costs 1; any delete+insert of a leaf costs 2.
  assert.strictEqual(r.cost, 1);
  assert.strictEqual(opsOf(r.patch, 'move').length, 1);
});

test('reparent move across parents', () => {
  const s = N('a', {}, [N('b', {}, [N('c', { value: 'v' })]), N('d')]);
  const t = N('a', {}, [N('b'), N('d', {}, [N('c', { value: 'v' })])]);
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  assert.strictEqual(r.optimal, true);
  assert.strictEqual(r.cost, 1); // one move beats delete+insert (2)
  assert.strictEqual(opsOf(r.patch, 'move').length, 1);
});

test('stable id forces long-distance match', () => {
  const s = N('root', {}, [
    N('sec', { id: 's1' }, [N('para', { id: 'p1', value: 'hello' })]),
    N('sec', { id: 's2' }),
  ]);
  const t = N('root', {}, [
    N('sec', { id: 's2' }),
    N('sec', { id: 's1' }),
    N('appendix', {}, [N('para', { id: 'p1', value: 'hello' })]),
  ]);
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  // p1 must be matched by id and moved under appendix, not deleted+inserted.
  const moves = opsOf(r.patch, 'move');
  assert.ok(moves.length >= 1);
  assert.ok(!r.patch.some((o) => o.op === 'insert' && o.node.type === 'para'));
});

test('id present on one side only is not matched away', () => {
  const s = N('a', {}, [N('b', { id: 'gone' })]);
  const t = N('a', {}, [N('b')]);
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  // The id-carrying node is deleted and a fresh anonymous node inserted.
  assert.strictEqual(opsOf(r.patch, 'delete').length, 1);
  assert.strictEqual(opsOf(r.patch, 'insert').length, 1);
});

test('ancestor-cycle matching is normalized away', () => {
  // Forced pairs: a#1<->a#1, b#2<->b#2, x#3<->x#3. Realizing all of them
  // would require moving a#1 into its own descendant b#2.
  const s = N('a', { id: '1' }, [N('b', { id: '2' }, [N('x', { id: '3' })])]);
  const t = N('b', { id: '2' }, [N('a', { id: '1' }, [N('x', { id: '3' })])]);
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  const out = applyPatch(s, r.patch);
  assert.strictEqual(countNodes(out), 3);
});

test('root type change is an update', () => {
  const s = N('old', { value: '1' });
  const t = N('new', { value: '1' });
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  assert.strictEqual(r.cost, 1);
  assert.strictEqual(opsOf(r.patch, 'update').length, 1);
});

test('budget interruption returns complete patch, flagged not optimal', () => {
  const s = N('a', {}, [N('x'), N('y'), N('z'), N('w')]);
  const t = N('a', {}, [N('w'), N('z'), N('y'), N('x')]);
  const r = diff(s, t, { budget: 1 });
  assert.strictEqual(r.optimal, false);
  assert.ok(verifyPatch(s, r.patch, t)); // still complete and usable
});

test('budget zero returns the greedy patch, flagged not optimal', () => {
  const s = N('a', {}, [N('x'), N('y')]);
  const t = N('a', {}, [N('y'), N('x')]);
  const r = diff(s, t, { budget: 0 });
  assert.strictEqual(r.optimal, false);
  assert.ok(verifyPatch(s, r.patch, t));
});

test('applier rejects malformed patches', () => {
  const s = N('a', {}, [N('b')]);
  assert.throws(() => applyPatch(s, [{ op: 'delete', path: [5] }]), PatchError);
  assert.throws(() => applyPatch(s, [{ op: 'delete', path: [] }]), PatchError);
  assert.throws(() => applyPatch(s, [{ op: 'move', from: [], parent: [], index: 0 }]), PatchError);
  assert.throws(
    () => applyPatch(s, [{ op: 'insert', parent: [0], index: 9, node: N('x') }]),
    PatchError,
  );
});

test('verifyPatch returns false on wrong target', () => {
  const s = N('a', {}, [N('b')]);
  const t = N('a', {}, [N('c')]);
  const r = diff(s, t);
  assert.strictEqual(verifyPatch(s, r.patch, N('a', {}, [N('d')])), false);
});

test('move op convention: index interpreted after detach', () => {
  // Hand-written patch: move first child to the end.
  const s = N('a', {}, [N('x'), N('y'), N('z')]);
  const out = applyPatch(s, [{ op: 'move', from: [0], parent: [], index: 2 }]);
  assert.deepStrictEqual(out, N('a', {}, [N('y'), N('z'), N('x')]));
});


test('move convention: detach shifts later sibling paths', () => {
  // x moves from under c ([1,0]) to under b ([0]); and a sibling move where
  // the detached node sits before its new parent in the root's child list.
  const s = N('a', {}, [N('b'), N('c', {}, [N('x')]), N('d')]);
  const t = N('a', {}, [N('b', {}, [N('x')]), N('c'), N('d')]);
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  assert.strictEqual(r.cost, 1);
  // Detaching [0] shifts the new parent [1] -> [0] in the evolving tree.
  const s2 = N('a', {}, [N('x'), N('b')]);
  const t2 = N('a', {}, [N('b', {}, [N('x')])]);
  const r2 = diff(s2, t2);
  assert.ok(verifyPatch(s2, r2.patch, t2));
  assert.strictEqual(r2.cost, 1);
  assert.strictEqual(opsOf(r2.patch, 'move').length, 1);
});

test('root id change is applied by update', () => {
  const s = N('a', { id: 'old' });
  const t = N('a', { id: 'new' });
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  assert.strictEqual(r.cost, 1);
  assert.strictEqual(opsOf(r.patch, 'update').length, 1);
});

test('duplicate ids pair k-th with k-th', () => {
  const s = N('r', {}, [N('a', { id: 'x', value: '1' }), N('a', { id: 'x', value: '2' })]);
  const t = N('r', {}, [N('a', { id: 'x', value: '2' }), N('a', { id: 'x', value: '1' })]);
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  // k-th pairing forces a reorder: one move (or two updates) -- either way
  // the patch must apply and stay minimal.
  const exh = exhaustiveMinCost(s, t);
  assert.strictEqual(r.cost, exh.minCost);
});

test('empty-ish trees: single-node vs deep chain', () => {
  const s = N('a');
  const t = N('b', {}, [N('c', {}, [N('d')])]);
  const r = diff(s, t);
  assert.ok(verifyPatch(s, r.patch, t));
  const exh = exhaustiveMinCost(s, t);
  assert.strictEqual(r.cost, exh.minCost);
});

// ------------------------------------------------------- exhaustive sweeps

test('exhaustive: all tree pairs up to 3 nodes, 2 types', () => {
  const trees = allTrees(3, ['a', 'b']);
  assert.strictEqual(trees.length, 22);
  let multiSolution = 0;
  for (const s of trees) {
    for (const t of trees) {
      const rep = checkDeterminism(s, t, 2, 50000);
      assert.ok(rep.ok, `problems: ${rep.problems.join('; ')}`);
      assert.strictEqual(rep.optimal, true, 'small trees must be proven optimal');
      assert.strictEqual(rep.cost, rep.minCost);
      if (rep.equivalentSolutions > 1) multiSolution++;
    }
  }
  // The sweep must actually exercise tie-breaking among equivalent optima.
  assert.ok(multiSolution > 0, 'expected pairs with multiple optimal solutions');
});

test('exhaustive: seeded random pairs up to 5 nodes', () => {
  const rand = prng(20261005);
  const opts = {
    maxDepth: 3,
    maxChildren: 3,
    types: ['a', 'b'],
    withValues: true,
    idPool: [] as string[],
    maxNodes: 5,
  };
  for (let i = 0; i < 120; i++) {
    const s = randomTree(rand, opts);
    const t = randomTree(rand, opts);
    const rep = checkDeterminism(s, t, 2, 100000);
    assert.ok(rep.ok, `pair ${i}: ${rep.problems.join('; ')}`);
  }
});

test('exhaustive: pairs with stable ids', () => {
  const rand = prng(777);
  const opts = {
    maxDepth: 3,
    maxChildren: 2,
    types: ['a', 'b'],
    withValues: false,
    idPool: ['k0', 'k1', 'k2', 'k3'],
    maxNodes: 5,
  };
  for (let i = 0; i < 60; i++) {
    const s = randomTree(rand, opts);
    const t = randomTree(rand, opts);
    const rep = checkDeterminism(s, t, 2, 100000);
    assert.ok(rep.ok, `pair ${i}: ${rep.problems.join('; ')}`);
  }
});

test('regression: random pairs verify and are deterministic', () => {
  const rand = prng(424242);
  const opts = {
    maxDepth: 4,
    maxChildren: 3,
    types: ['a', 'b', 'c'],
    withValues: true,
    idPool: ['u0', 'u1', 'u2'],
    maxNodes: 9,
  };
  for (let i = 0; i < 150; i++) {
    const s = randomTree(rand, opts);
    const t = randomTree(rand, opts);
    const r1 = diff(s, t, { budget: 3000 });
    const r2 = diff(s, t, { budget: 3000 });
    assert.ok(verifyPatch(s, r1.patch, t), `pair ${i}: patch does not verify`);
    assert.deepStrictEqual(r1.patch, r2.patch, `pair ${i}: nondeterministic`);
    assert.strictEqual(r1.cost, r2.cost);
    // If proven optimal, a re-run with a bigger budget must not improve.
    if (r1.optimal) {
      const r3 = diff(s, t, { budget: 50000 });
      assert.strictEqual(r3.cost, r1.cost, `pair ${i}: optimal claim contradicted`);
    }
  }
});

test('exhaustive enumerator agrees with itself on a known case', () => {
  const s = N('a', {}, [N('b'), N('c')]);
  const t = N('a', {}, [N('c'), N('b')]);
  const exh = exhaustiveMinCost(s, t);
  assert.strictEqual(exh.minCost, 1); // one move
  assert.ok(!exh.truncated);
});

// ------------------------------------------------------------------ summary

console.log(`\n${passed} passed, ${failed} failed`);
if (failures.length > 0) {
  for (const f of failures) console.log(`  FAIL ${f}`);
  process.exit(1);
}
