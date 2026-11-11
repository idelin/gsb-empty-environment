import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { parseJsonTree, type JsonValue } from '../src/json-parser.ts';
import {
  ConfigStore,
  MergeError,
  encodePointerSegment,
  isParsedTree,
  mergeConfigs,
} from '../src/merge-config.ts';
import { assertStructurallyEqual } from './helpers.ts';

const tree = (text: string, sourceName?: string) =>
  parseJsonTree(text, { sourceName: sourceName ?? text.slice(0, 24) });

const snapshot = (value: JsonValue): JsonValue =>
  JSON.parse(JSON.stringify(value)) as JsonValue;

describe('mergeConfigs', () => {
  it('merges records in order with later sources winning scalar leaves', () => {
    const result = mergeConfigs([
      { tree: tree('{"a":1,"b":{"x":1,"y":2}}', 'base') },
      { tree: tree('{"b":{"y":9,"z":3},"c":true}', 'override') },
    ]);
    assertStructurallyEqual(result.root, { a: 1, b: { x: 1, y: 9, z: 3 }, c: true });
    assert.equal(Object.getPrototypeOf(result.root), null);
    assert.equal(Object.getPrototypeOf((result.root as any).b), null);
  });

  it('keeps a JSON-pointer provenance path for every leaf', () => {
    const result = mergeConfigs([
      { tree: tree('{"a":1,"b":{"x":1}}', 'base') },
      { tree: tree('{"b":{"x":2},"d":null}', 'override') },
    ]);
    assert.equal(result.origins.get('/a'), 'base');
    assert.equal(result.origins.get('/b/x'), 'override');
    assert.equal(result.origins.get('/d'), 'override');
    assert.equal(result.origins.size, 3);
  });

  it('encodes escaped pointer segments per RFC 6901', () => {
    assert.equal(encodePointerSegment('a/b'), 'a~1b');
    assert.equal(encodePointerSegment('a~b'), 'a~0b');
    const result = mergeConfigs([
      { tree: tree('{"a/b":{"c~d":7}}', 'esc') },
    ]);
    assert.equal(result.origins.get('/a~1b/c~0d'), 'esc');
    assert.equal((result.root as any)['a/b']['c~d'], 7);
  });

  it('replaces arrays by integer index while preserving tail elements', () => {
    const result = mergeConfigs([
      { tree: tree('{"list":[{"k":1},{"k":2},{"k":3}]}', 'base') },
      { tree: tree('{"list":[{"k":10}]}', 'override') },
    ]);
    assertStructurallyEqual(result.root, { list: [{ k: 10 }, { k: 2 }, { k: 3 }] });
    assert.equal(result.origins.get('/list/0/k'), 'override');
    assert.equal(result.origins.get('/list/1/k'), 'base');
  });

  it('can wholesale-replace arrays when index merging is disabled', () => {
    const result = mergeConfigs(
      [
        { tree: tree('{"list":[1,2,3]}', 'base') },
        { tree: tree('{"list":[9]}', 'override') },
      ],
      { indexReplaceArrays: false },
    );
    assertStructurallyEqual(result.root, { list: [9] });
    assert.equal(result.origins.get('/list/0'), 'override');
    assert.equal(result.origins.has('/list/1'), false);
  });

  it('replaces non-array values wholesale and prunes stale provenance', () => {
    const result = mergeConfigs([
      { tree: tree('{"a":{"deep":{"leaf":1}}}', 'base') },
      { tree: tree('{"a":5}', 'override') },
    ]);
    assertStructurallyEqual(result.root, { a: 5 });
    assert.equal(result.origins.get('/a'), 'override');
    assert.equal(result.origins.has('/a/deep/leaf'), false);
  });

  it('never mutates the parsed input trees', () => {
    const base = tree('{"a":{"x":1},"list":[1,2]}', 'base');
    const override = tree('{"a":{"y":2},"list":[9]}', 'override');
    const baseBefore = snapshot(base.root);
    const overrideBefore = snapshot(override.root);
    mergeConfigs([{ tree: base }, { tree: override }]);
    assertStructurallyEqual(base.root, baseBefore);
    assertStructurallyEqual(override.root, overrideBefore);
  });

  it('freezes the entire published tree', () => {
    const result = mergeConfigs([{ tree: tree('{"a":{"b":[1]}}', 's') }]);
    assert.ok(Object.isFrozen(result.root));
    assert.ok(Object.isFrozen((result.root as any).a));
    assert.ok(Object.isFrozen((result.root as any).a.b));
    assert.throws(() => {
      (result.root as any).a.b.push(2);
    }, TypeError);
  });

  it('rejects dangerous key segments at any depth', () => {
    const attacks = [
      '{"__proto__":{"polluted":true}}',
      '{"constructor":{"prototype":{"polluted":true}}}',
      '{"safe":{"prototype":{"polluted":true}}}',
      '{"a":[{"__proto__":{"polluted":true}}]}',
    ];
    for (const text of attacks) {
      assert.throws(
        () => mergeConfigs([{ tree: tree(text, 'attack') }]),
        (error: unknown) => error instanceof MergeError && /dangerous key segment/.test(error.message),
        `should reject ${text}`,
      );
    }
  });

  it('only accepts branded ParsedTree inputs', () => {
    assert.throws(() => mergeConfigs([]), MergeError);
    assert.throws(
      () => mergeConfigs([{ tree: { root: { a: 1 } } as any }]),
      (error: unknown) => error instanceof MergeError && /ParsedTree/.test(error.message),
    );
    assert.ok(isParsedTree(tree('{}')));
    assert.equal(isParsedTree({}), false);
  });

  it('requires an object at the JSON root', () => {
    assert.throws(
      () => mergeConfigs([{ tree: tree('[1,2,3]', 'array-root') }]),
      /Top-level JSON value.*must be an object/,
    );
  });
});

describe('ConfigStore', () => {
  it('publishes atomically and leaves the old config untouched on failure', () => {
    const store = new ConfigStore();
    store.publish([{ tree: tree('{"a":1,"b":{"x":1},"list":[1,2]}', 'v1') }]);
    const oldConfig = store.config;
    const oldOrigins = new Map(store.provenance);
    const oldSnapshot = snapshot(oldConfig);

    assert.throws(() =>
      store.publish([
        { tree: tree('{"b":{"y":2}}', 'v2') },
        { tree: tree('{"__proto__":{"polluted":true}}', 'attack') },
      ]),
    );

    assert.equal(store.config, oldConfig);
    assertStructurallyEqual(store.config, oldSnapshot);
    assert.deepEqual(new Map(store.provenance), oldOrigins);
    assert.equal((store.config as any).b.y, undefined);
  });

  it('accumulates over the active config by default and tracks new provenance', () => {
    const store = new ConfigStore();
    store.publish([{ tree: tree('{"a":1,"b":1}', 'v1') }]);
    store.publish([{ tree: tree('{"b":2,"c":3}', 'v2') }]);
    assertStructurallyEqual(store.config, { a: 1, b: 2, c: 3 });
    assert.equal(store.getOrigin('/a'), '<previous-config>');
    assert.equal(store.getOrigin('/b'), 'v2');
    assert.equal(store.getOrigin('/c'), 'v2');
  });

  it('supports non-accumulating replacement publishes', () => {
    const store = new ConfigStore();
    store.publish([{ tree: tree('{"a":1}', 'v1') }]);
    store.publish([{ tree: tree('{"z":9}', 'v2') }], { accumulate: false });
    assertStructurallyEqual(store.config, { z: 9 });
    assert.equal(store.getOrigin('/a'), undefined);
    assert.equal(store.getOrigin('/z'), 'v2');
  });

  it('buildInIsolation is a dry run that never publishes', () => {
    const store = new ConfigStore();
    store.publish([{ tree: tree('{"a":1}', 'v1') }]);
    const before = store.config;
    const draft = store.buildInIsolation([{ tree: tree('{"a":2}', 'draft') }]);
    assert.equal(store.config, before);
    assert.equal((store.config as any).a, 1);
    assert.equal((draft.root as any).a, 2);
    assert.ok(Object.isFrozen(draft.root));
  });

  it('handles sparse-array substitutes as plain index data without magic keys', () => {
    const store = new ConfigStore();
    store.publish([
      {
        tree: tree(
          '{"arr":[10,20,30],"arrayLike":{"0":"zero","2":"two","length":3}}',
          'v1',
        ),
      },
    ]);
    const second = store.buildInIsolation([
      { tree: tree('{"arr":[99,null,33,44],"arrayLike":{"1":"one"}}', 'v2') },
    ]);
    assert.deepEqual((second.root as any).arr, [99, null, 33, 44]);
    for (const index of [0, 1, 2, 3]) {
      assert.equal(second.origins.get(`/arr/${index}`), 'v2');
    }
    assertStructurallyEqual((second.root as any).arrayLike, {
      0: 'zero',
      1: 'one',
      2: 'two',
      length: 3,
    });
    assert.equal(Object.getPrototypeOf((second.root as any).arrayLike), null);
  });
});
