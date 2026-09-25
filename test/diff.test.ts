import test from 'node:test';
import assert from 'node:assert/strict';
import { diffTrees } from '../src/diff.ts';
import { applyPatch } from '../src/apply.ts';
import { TreeError, validateTree } from '../src/validate.ts';
import { canonicalize } from '../src/util.ts';
import type { Op, TreeNode } from '../src/types.ts';

const node = (
  id: string,
  attrs?: TreeNode['attrs'],
  children?: TreeNode[],
): TreeNode => ({ id, ...(attrs ? { attrs } : {}), ...(children ? { children } : {}) });

function applyAndAssertEqual(oldTree: TreeNode | null, newTree: TreeNode | null): Op[] {
  const patch = diffTrees(oldTree, newTree);
  const applied = applyPatch(oldTree, patch);
  assert.deepEqual(canonicalize(applied), canonicalize(newTree));
  return patch;
}

test('attribute update is minimal: set changed, unset removed, rest untouched', () => {
  const before = node('r', { a: 1, b: 2, keep: 'x' });
  const after = node('r', { a: 10, c: [1, 2], keep: 'x' });
  const patch = diffTrees(before, after);
  assert.deepEqual(patch, [
    { op: 'update', id: 'r', set: { a: 10, c: [1, 2] }, unset: ['b'] },
  ]);
  applyAndAssertEqual(before, after);
});

test('identical trees produce an empty patch', () => {
  const t = node('r', { a: 1 }, [node('c', { b: [1, { x: true }] })]);
  assert.deepEqual(diffTrees(t, t), []);
});

test('insert of a subtree creates parents before children', () => {
  const before = node('root');
  const after = node('root', undefined, [
    node('p', { kind: 'dir' }, [node('c1'), node('c2', { v: 1 })]),
  ]);
  const patch = applyAndAssertEqual(before, after);
  const inserts = patch.filter((op) => op.op === 'insert');
  assert.deepEqual(
    inserts.map((op) => (op.op === 'insert' ? op.id : '')),
    ['p', 'c1', 'c2'],
  );
  // Every parent is inserted before its children.
  const position = new Map(inserts.map((op, i) => [op.op === 'insert' ? op.id : '', i]));
  for (const op of inserts) {
    if (op.op === 'insert' && op.parentId !== null && position.has(op.parentId)) {
      assert.ok((position.get(op.parentId) as number) < (position.get(op.id) as number));
    }
  }
});

test('cross-parent move preserves identity (move, not remove+insert)', () => {
  const before = node('root', undefined, [
    node('a', undefined, [node('x', { v: 1 })]),
    node('b'),
  ]);
  const after = node('root', undefined, [
    node('a'),
    node('b', undefined, [node('x', { v: 1 })]),
  ]);
  const patch = applyAndAssertEqual(before, after);
  assert.deepEqual(patch, [{ op: 'move', id: 'x', parentId: 'b', index: 0 }]);
});

test('deleting a parent does not re-delete a child that moved out', () => {
  const before = node('root', undefined, [
    node('doomed', undefined, [node('survivor'), node('goner')]),
    node('safe'),
  ]);
  const after = node('root', undefined, [node('safe', undefined, [node('survivor')])]);
  const patch = applyAndAssertEqual(before, after);
  assert.deepEqual(patch, [
    { op: 'move', id: 'survivor', parentId: 'safe', index: 0 },
    { op: 'remove', id: 'doomed' },
  ]);
  assert.ok(!patch.some((op) => op.op === 'remove' && op.id === 'survivor'));
});

test('nested deletes collapse to the topmost remove only', () => {
  const before = node('root', undefined, [
    node('sub', undefined, [node('a', undefined, [node('b')])]),
  ]);
  const after = node('root');
  const patch = applyAndAssertEqual(before, after);
  assert.deepEqual(patch, [{ op: 'remove', id: 'sub' }]);
});

test('same-parent reorder moves the minimal set (LIS complement)', () => {
  const before = node('root', undefined, [node('a'), node('b'), node('c'), node('d')]);
  const after = node('root', undefined, [node('a'), node('d'), node('b'), node('c')]);
  const patch = applyAndAssertEqual(before, after);
  assert.deepEqual(patch, [{ op: 'move', id: 'd', parentId: 'root', index: 1 }]);
});

test('root replacement and empty-tree edges', () => {
  const t = node('r', { a: 1 }, [node('c')]);
  // null -> tree
  let patch = applyAndAssertEqual(null, t);
  assert.equal(patch[0].op, 'insert');
  assert.equal(patch[0].op === 'insert' ? patch[0].parentId : 'x', null);
  // tree -> null
  patch = applyAndAssertEqual(t, null);
  assert.deepEqual(patch, [{ op: 'remove', id: 'r' }]);
  // root id changed = remove old root + insert new root
  patch = applyAndAssertEqual(node('old'), node('new'));
  assert.deepEqual(patch, [
    { op: 'insert', id: 'new', parentId: null, index: 0, attrs: {} },
    { op: 'remove', id: 'old' },
  ]);
});

test('patch is deterministic regardless of JSON key order', () => {
  const build = (reverse: boolean): { a: TreeNode; b: TreeNode } => {
    const keys = <T extends object>(obj: T): T => {
      const out: Record<string, unknown> = {};
      const ks = Object.keys(obj);
      if (reverse) ks.reverse();
      for (const k of ks) out[k] = (obj as Record<string, unknown>)[k];
      return out as T;
    };
    const a = keys({
      id: 'root',
      children: [
        keys({ id: 'p', attrs: keys({ x: 1, y: 2 }), children: [keys({ id: 'q', attrs: keys({ m: 0 }) })] }),
        keys({ id: 'gone', children: [keys({ id: 'keep' })] }),
      ],
    }) as TreeNode;
    const b = keys({
      id: 'root',
      children: [
        keys({ id: 'p', children: [keys({ id: 'q', attrs: keys({ m: 1, n: 2 }) }), keys({ id: 'keep' })] }),
        keys({ id: 'fresh', attrs: keys({ z: 9 }) }),
      ],
    }) as TreeNode;
    return { a, b };
  };
  const first = build(false);
  const second = build(true);
  const patch1 = diffTrees(first.a, first.b);
  const patch2 = diffTrees(second.a, second.b);
  assert.equal(JSON.stringify(patch1), JSON.stringify(patch2));
  assert.deepEqual(canonicalize(applyPatch(first.a, patch1)), canonicalize(first.b));
});

test('validation errors carry precise paths', () => {
  assert.throws(
    () => validateTree({ id: 'r', children: [{ id: 'a' }, { id: 'a' }] }),
    (err: unknown) => err instanceof TreeError && err.path === '/children/1/id',
  );
  assert.throws(
    () => validateTree({ id: 'r', attrs: { bad: Number.NaN } }),
    (err: unknown) => err instanceof TreeError && err.path === '/attrs/bad',
  );
  assert.throws(
    () => validateTree({ id: 'r', children: [{ id: 'c', oops: 1 }] }),
    (err: unknown) => err instanceof TreeError && err.path === '/children/0/oops',
  );
  assert.throws(
    () => validateTree({ id: 'r', children: {} }),
    (err: unknown) => err instanceof TreeError && err.path === '/children',
  );
  assert.throws(
    () => validateTree({ id: 'r', attrs: { fn: () => 1 } }),
    (err: unknown) => err instanceof TreeError && err.path === '/attrs/fn',
  );
});

test('apply errors locate the offending op', () => {
  assert.throws(
    () => applyPatch(node('r'), [{ op: 'remove', id: 'missing' }]),
    (err: unknown) => err instanceof TreeError && err.path === '/ops/0',
  );
  assert.throws(
    () =>
      applyPatch(node('r'), [
        { op: 'insert', id: 'n', parentId: 'nope', index: 0, attrs: {} },
      ]),
    (err: unknown) => err instanceof TreeError && err.path === '/ops/0',
  );
});

test('applyPatch does not mutate the input tree', () => {
  const before = node('r', { a: 1 }, [node('c')]);
  const snapshot = JSON.stringify(before);
  applyPatch(before, diffTrees(before, node('r', { a: 2 })));
  assert.equal(JSON.stringify(before), snapshot);
});

// --- property-style round trip: apply(diff(a, b), a) === b ----------------

function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomTree(rng: () => number, nextId: () => string, depth: number): TreeNode {
  const n = node(nextId());
  if (rng() < 0.7) {
    n.attrs = {};
    const count = Math.floor(rng() * 3);
    for (let i = 0; i < count; i++) {
      n.attrs[`k${Math.floor(rng() * 5)}`] =
        rng() < 0.5 ? Math.floor(rng() * 100) : `s${Math.floor(rng() * 10)}`;
    }
  }
  if (depth < 4 && rng() < 0.75) {
    const count = Math.floor(rng() * 4);
    n.children = [];
    for (let i = 0; i < count; i++) n.children.push(randomTree(rng, nextId, depth + 1));
  }
  return n;
}

function collectIds(t: TreeNode | null, out: string[] = []): string[] {
  if (t === null) return out;
  out.push(t.id);
  for (const c of t.children ?? []) collectIds(c, out);
  return out;
}

function findNode(t: TreeNode | null, id: string): TreeNode | null {
  if (t === null) return null;
  if (t.id === id) return t;
  for (const c of t.children ?? []) {
    const hit = findNode(c, id);
    if (hit) return hit;
  }
  return null;
}

function isDescendant(ancestor: TreeNode, id: string): boolean {
  return (ancestor.children ?? []).some((c) => c.id === id || isDescendant(c, id));
}

/** Randomly mutate a tree: attr edits, moves, deletes, inserts, reorders. */
function mutate(root: TreeNode, rng: () => number, nextId: () => string): TreeNode {
  const copy: TreeNode = JSON.parse(JSON.stringify(root));
  const steps = 1 + Math.floor(rng() * 6);
  for (let s = 0; s < steps; s++) {
    const ids = collectIds(copy);
    const pick = ids[Math.floor(rng() * ids.length)];
    const target = findNode(copy, pick) as TreeNode;
    const action = Math.floor(rng() * 5);
    if (action === 0) {
      target.attrs = target.attrs ?? {};
      target.attrs[`m${Math.floor(rng() * 4)}`] = Math.floor(rng() * 50);
      if (rng() < 0.3) {
        const keys = Object.keys(target.attrs);
        if (keys.length > 0) delete target.attrs[keys[0]];
      }
    } else if (action === 1 && ids.length > 1 && pick !== copy.id) {
      // move pick under a random non-descendant parent
      const parents = ids.filter((id) => id !== pick && !isDescendant(target, id));
      const newParentId = parents[Math.floor(rng() * parents.length)];
      const detachFrom = (n: TreeNode): boolean => {
        const kids = n.children ?? [];
        const at = kids.findIndex((k) => k.id === pick);
        if (at >= 0) {
          kids.splice(at, 1);
          return true;
        }
        return kids.some(detachFrom);
      };
      detachFrom(copy);
      const parent = findNode(copy, newParentId) as TreeNode;
      parent.children = parent.children ?? [];
      parent.children.splice(Math.floor(rng() * (parent.children.length + 1)), 0, target);
    } else if (action === 2 && ids.length > 1 && pick !== copy.id) {
      const detachFrom = (n: TreeNode): boolean => {
        const kids = n.children ?? [];
        const at = kids.findIndex((k) => k.id === pick);
        if (at >= 0) {
          kids.splice(at, 1);
          return true;
        }
        return kids.some(detachFrom);
      };
      detachFrom(copy);
    } else if (action === 3) {
      const fresh = randomTree(rng, nextId, 3);
      target.children = target.children ?? [];
      target.children.splice(Math.floor(rng() * (target.children.length + 1)), 0, fresh);
    } else if (target.children && target.children.length > 1) {
      for (let i = target.children.length - 1; i > 0; i--) {
        const j = Math.floor(rng() * (i + 1));
        [target.children[i], target.children[j]] = [target.children[j], target.children[i]];
      }
    }
  }
  return copy;
}

test('round trip: apply(diff(a, b), a) deep-equals b for 300 random cases', () => {
  const rng = mulberry32(20260926);
  let counter = 0;
  const nextId = (): string => `n${counter++}`;
  for (let iter = 0; iter < 300; iter++) {
    const before = randomTree(rng, nextId, 0);
    const after = mutate(before, rng, nextId);
    const patch = diffTrees(before, after);
    const applied = applyPatch(before, patch);
    assert.deepEqual(
      canonicalize(applied),
      canonicalize(after),
      `round trip failed at iteration ${iter}`,
    );
    // No redundant removes: every removed id is absent from the new tree.
    const afterIds = new Set(collectIds(after));
    for (const op of patch) {
      if (op.op === 'remove') assert.ok(!afterIds.has(op.id));
    }
  }
});
