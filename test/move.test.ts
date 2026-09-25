import { test } from 'node:test';
import assert from 'node:assert/strict';
import { movePaths, planMoves, MoveConflictError } from '../src/index.ts';
import type { JsonValue } from '../src/index.ts';

test('a/* -> b/* moves every child and removes the source', () => {
  const { tree, plan } = movePaths({ a: { x: 1, y: 2 }, keep: true }, [{ from: 'a/*', to: 'b/*' }]);
  assert.deepEqual(tree, { keep: true, b: { x: 1, y: 2 } });
  assert.deepEqual(
    plan.map((op) => [op.fromPath, op.toPath]),
    [
      ['a/x', 'b/x'],
      ['a/y', 'b/y'],
    ],
  );
});

test('conflict: target already exists with a different value', () => {
  assert.throws(
    () => movePaths({ a: { x: 1 }, b: { x: 2 } }, [{ from: 'a/*', to: 'b/*' }]),
    (err: unknown) => {
      assert.ok(err instanceof MoveConflictError);
      const conflict = err as MoveConflictError;
      assert.equal(conflict.targetPath, 'b/x');
      assert.deepEqual(conflict.sourcePaths, ['a/x']);
      assert.match(conflict.message, /different value/);
      return true;
    },
  );
});

test('identical value already at target is a no-op, not a conflict', () => {
  const { tree } = movePaths({ a: { x: 1 }, b: { x: 1 } }, [{ from: 'a/*', to: 'b/*' }]);
  assert.deepEqual(tree, { b: { x: 1 } });
});

test('keys containing slashes are matched and emitted via escaping', () => {
  const input = { 'a/b': { 'c/d': 1 }, other: 2 };
  const { tree, plan } = movePaths(input, [{ from: 'a\\/b/*', to: 'out/*' }]);
  assert.deepEqual(tree, { other: 2, out: { 'c/d': 1 } });
  assert.equal(plan[0]?.fromPath, 'a\\/b/c\\/d');
  assert.equal(plan[0]?.toPath, 'out/c\\/d');
});

test('ancestor -> descendant (a -> a/b) terminates without infinite recursion', () => {
  const { tree } = movePaths({ a: { x: 1 } }, [{ from: 'a', to: 'a/b' }]);
  assert.deepEqual(tree, { a: { b: { x: 1 } } });
});

test('descendant -> ancestor with different value is a located conflict', () => {
  assert.throws(
    () => movePaths({ a: { b: 1, c: 2 } }, [{ from: 'a/b', to: 'a' }]),
    (err: unknown) => {
      assert.ok(err instanceof MoveConflictError);
      assert.equal((err as MoveConflictError).targetPath, 'a');
      return true;
    },
  );
});

test('plan-first: simultaneous moves do not chain or rewrite new nodes', () => {
  const { tree } = movePaths({ a: 1, b: 2 }, [
    { from: 'a', to: 'b' },
    { from: 'b', to: 'c' },
  ]);
  assert.deepEqual(tree, { b: 1, c: 2 });
});

test('a -> a/b -> ... style self-nesting rule does not loop', () => {
  const { tree, plan } = movePaths({ a: 1 }, [{ from: 'a', to: 'a/b/c' }]);
  assert.deepEqual(tree, { a: { b: { c: 1 } } });
  assert.equal(plan.length, 1);
});

test('arrays stay ordered; * matches indices', () => {
  const input = { list: ['a', 'b', 'c'] };
  const { tree } = movePaths(input, [{ from: 'list/*', to: 'copy/*' }]);
  assert.deepEqual(tree, { copy: ['a', 'b', 'c'] });
});

test('removing some array elements compacts the rest in order', () => {
  const { tree } = movePaths({ list: [1, 2, 3] }, [{ from: 'list/0', to: 'picked' }]);
  assert.deepEqual(tree, { list: [2, 3], picked: 1 });
});

test('glob: src/** matches src itself and moves it wholesale', () => {
  const input = { src: { a: { deep: 1 }, b: 2 } };
  const { tree } = movePaths(input, [{ from: 'src/**', to: 'dst/**' }]);
  assert.deepEqual(tree, { dst: { a: { deep: 1 }, b: 2 } });
});

test('glob in the middle captures and rebuilds nested paths', () => {
  const input = { a: { x: { z: 1 }, y: { z: 2 } } };
  const { tree, plan } = movePaths(input, [{ from: 'a/**/z', to: 'b/**/z' }]);
  assert.deepEqual(tree, { b: { x: { z: 1 }, y: { z: 2 } } });
  assert.deepEqual(
    plan.map((op) => [op.fromPath, op.toPath]),
    [
      ['a/x/z', 'b/x/z'],
      ['a/y/z', 'b/y/z'],
    ],
  );
});

test('merging into an existing different subtree conflicts', () => {
  assert.throws(
    () => movePaths({ src: { v: 1 }, dst: { other: 2 } }, [{ from: 'src/**', to: 'dst/**' }]),
    (err: unknown) => {
      assert.ok(err instanceof MoveConflictError);
      assert.equal((err as MoveConflictError).targetPath, 'dst');
      return true;
    },
  );
});

test('multiple captures are substituted in order', () => {
  const input = { a: { m: { n: { b: 7 } } } };
  const { tree } = movePaths(input, [{ from: 'a/*/*/b', to: 'x/*/*' }]);
  assert.deepEqual(tree, { x: { m: { n: 7 } } });
});

test('first matching rule wins; matched subtrees are not re-descended', () => {
  const input = { a: { x: 1 } };
  const { tree } = movePaths(input, [
    { from: 'a', to: 'b' },
    { from: 'a/x', to: 'c' },
  ]);
  assert.deepEqual(tree, { b: { x: 1 } });
});

test('duplicate targets with different values conflict, equal values merge', () => {
  assert.throws(
    () => movePaths({ p: 1, q: 2 }, [
      { from: 'p', to: 'z' },
      { from: 'q', to: 'z' },
    ]),
    (err: unknown) => {
      assert.ok(err instanceof MoveConflictError);
      assert.equal((err as MoveConflictError).targetPath, 'z');
      assert.deepEqual((err as MoveConflictError).sourcePaths.sort(), ['p', 'q']);
      return true;
    },
  );
  const { tree } = movePaths({ p: 5, q: 5 }, [
    { from: 'p', to: 'z' },
    { from: 'q', to: 'z' },
  ]);
  assert.deepEqual(tree, { z: 5 });
});

test('nested targets conflict (one target inside another)', () => {
  assert.throws(
    () => movePaths({ a: { x: 1 }, b: 2 }, [
      { from: 'a', to: 't' },
      { from: 'b', to: 't/y' },
    ]),
    (err: unknown) => {
      assert.ok(err instanceof MoveConflictError);
      assert.equal((err as MoveConflictError).targetPath, 't/y');
      return true;
    },
  );
});

test('input tree is never mutated', () => {
  const input: JsonValue = { a: { x: [1, 2] }, b: 3 };
  const snapshot = JSON.stringify(input);
  movePaths(input, [{ from: 'a/*', to: 'c/*' }]);
  assert.equal(JSON.stringify(input), snapshot);
});

test('plan is empty when nothing matches', () => {
  assert.deepEqual(planMoves({ a: 1 }, [{ from: 'nope/*', to: 'x/*' }]), []);
});

test('root-level move replaces the document', () => {
  const { tree } = movePaths({ a: 1 }, [{ from: '', to: 'doc' }]);
  assert.deepEqual(tree, { doc: { a: 1 } });
});

test('containers emptied by moves are pruned; pre-existing empties are kept', () => {
  const { tree } = movePaths({ a: { x: 1 }, already: {} }, [{ from: 'a/x', to: 'b' }]);
  assert.deepEqual(tree, { already: {}, b: 1 });
});
