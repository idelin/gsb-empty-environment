import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  canonicalize,
  formatPath,
  getAtPath,
  jsonEquals,
  parsePath,
} from '../src/json.ts';

describe('json paths', () => {
  it('formats nested object/array paths', () => {
    assert.equal(formatPath([]), '$');
    assert.equal(formatPath(['items', 2, 'name']), '$.items[2].name');
    assert.equal(formatPath(['weird key', 0]), '$["weird key"][0]');
  });

  it('round-trips through parsePath', () => {
    const paths: (string | number)[][] = [[], ['a'], ['items', 2, 'name'], ['weird key', 0, '$x']];
    for (const path of paths) {
      assert.deepEqual(parsePath(formatPath(path)), path);
    }
  });

  it('locates values in a tree', () => {
    const tree = { items: [{ name: 'a' }, { name: 'b' }, { name: 42 }] };
    assert.equal(getAtPath(tree, ['items', 2, 'name']), 42);
    assert.equal(getAtPath(tree, ['items', 9]), undefined);
    assert.equal(getAtPath(tree, ['missing', 'key']), undefined);
  });
});

describe('canonical json semantics', () => {
  it('treats object key order as insignificant', () => {
    const a = { x: 1, y: { p: 1, q: 2 } };
    const b = { y: { q: 2, p: 1 }, x: 1 };
    assert.equal(canonicalize(a), canonicalize(b));
    assert.ok(jsonEquals(a, b));
  });

  it('treats array order as significant', () => {
    assert.notEqual(canonicalize([1, 2]), canonicalize([2, 1]));
    assert.ok(!jsonEquals([1, 2], [2, 1]));
  });
});
