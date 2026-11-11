import assert from 'node:assert/strict';
import { after, before, describe, it } from 'node:test';

import { parseJsonTree } from '../src/json-parser.ts';
import { ConfigStore, MergeError, mergeConfigs } from '../src/merge-config.ts';
import { assertStructurallyEqual } from './helpers.ts';

/**
 * Attack payloads deliberately nest __proto__ / constructor / prototype at
 * different depths and inside arrays, mirroring classic merge-based
 * prototype-pollution gadgets.
 */
const ATTACK_PAYLOADS = [
  '{"__proto__":{"polluted":"yes"}}',
  '{"constructor":{"prototype":{"polluted":"yes"}}}',
  '{"a":{"b":{"__proto__":{"polluted":"yes"}}}}',
  '{"entries":[{"constructor":{"prototype":{"polluted":"yes"}}}]}',
  '{"__proto__":{"__proto__":{"polluted":"yes"}}}',
  '{"safe":{"prototype":{"constructor":{"polluted":"yes"}}}}',
];

const GLOBAL_PROBES = [
  { object: Object.prototype as Record<string, unknown>, key: 'polluted' },
  { object: Object.prototype as Record<string, unknown>, key: 'isAdmin' },
  { object: Array.prototype as unknown as Record<string, unknown>, key: 'polluted' },
] as const;

describe('global prototype invariants', () => {
  const protoSnapshots = new Map<object, string | undefined>();

  before(() => {
    for (const probe of GLOBAL_PROBES) {
      protoSnapshots.set(probe.object, Object.getOwnPropertyDescriptor(probe.object, probe.key) ? 'present' : 'absent');
    }
  });

  after(() => {
    for (const probe of GLOBAL_PROBES) {
      const had = protoSnapshots.get(probe.object);
      const now = Object.getOwnPropertyDescriptor(probe.object, probe.key) ? 'present' : 'absent';
      assert.equal(now, had, `global prototype property ${probe.key} changed`);
    }
    assert.equal(({} as any).polluted, undefined);
    assert.equal(([] as any).polluted, undefined);
  });

  it('rejects every nested special-key payload while merging', () => {
    for (const payload of ATTACK_PAYLOADS) {
      const tree = parseJsonTree(payload, { sourceName: 'attack' });
      assert.throws(
        () => mergeConfigs([{ tree }]),
        (error: unknown) => error instanceof MergeError && /dangerous key segment/.test(error.message),
        `payload should be refused: ${payload}`,
      );
    }
  });

  it('rejects special keys during ConfigStore publishes without side effects', () => {
    const store = new ConfigStore();
    store.publish([
      { tree: parseJsonTree('{"service":"tpl","defaults":{"timeout":30}}', { sourceName: 'baseline' }) },
    ]);

    for (const payload of ATTACK_PAYLOADS) {
      const configBefore = store.config;
      assert.throws(() =>
        store.publish([
          { tree: parseJsonTree(payload, { sourceName: 'attack' }) },
        ]),
      );
      assert.equal(store.config, configBefore, 'old config reference must survive a failed publish');
      assertStructurallyEqual(store.config, { service: 'tpl', defaults: { timeout: 30 } });
    }

    const plainObject: Record<string, unknown> = {};
    const plainArray: unknown[] = [];
    assert.equal(plainObject.polluted, undefined);
    assert.equal(plainObject.isAdmin, undefined);
    assert.equal(plainArray.polluted, undefined);
  });

  it('parses __proto__ as an inert own field, then refuses it at the merge boundary', () => {
    const tree = parseJsonTree('{"__proto__":{"safe":"data"}}', { sourceName: 'harmless-key' });
    const parsedRoot = tree.root as Record<string, unknown>;
    const descriptor = Object.getOwnPropertyDescriptor(parsedRoot, '__proto__');
    assert.ok(descriptor, '__proto__ is an ordinary own property of the null-prototype record');
    assert.deepEqual(Object.entries(descriptor?.value as object), [['safe', 'data']]);
    assert.equal(Object.getPrototypeOf(descriptor?.value as object), null);
    assert.equal(Object.getPrototypeOf(parsedRoot), null);
    assert.throws(
      () => mergeConfigs([{ tree, name: 'harmless-key' }]),
      (error: unknown) => error instanceof MergeError && /dangerous key segment "__proto__"/.test(error.message),
    );
    assert.equal(({} as any).safe, undefined);
  });

  it('treats sparse-array substitutes as inert records and index-only arrays', () => {
    const store = new ConfigStore();
    store.publish([
      {
        tree: parseJsonTree(
          '{"matrix":[{"id":"a"},{"id":"b"}],"arrayLike":{"0":1,"length":1}}',
          { sourceName: 'v1' },
        ),
      },
    ]);

    const attackTree = parseJsonTree(
      '{"matrix":[{"id":"x"}],"arrayLike":{"__proto__":{"polluted":"yes"},"0":2}}',
      { sourceName: 'v2' },
    );
    assert.throws(() => store.publish([{ tree: attackTree }]), MergeError);

    assert.deepEqual(JSON.parse(JSON.stringify(store.config)), {
      matrix: [{ id: 'a' }, { id: 'b' }],
      arrayLike: { 0: 1, length: 1 },
    });
    assert.equal(Array.isArray((store.config as any).arrayLike), false);
    assert.equal(Object.getPrototypeOf((store.config as any).arrayLike), null);
    assert.deepEqual(Array.from({ length: 2 }, (_, index) => index), [0, 1]);
  });

  it('depth ceiling blocks stack-exhaustion payloads before any merge work', () => {
    const depth = 8;
    const nestedObjects = '{"k":'.repeat(depth + 2) + '1' + '}'.repeat(depth + 2);
    const nestedArrays = '['.repeat(depth + 2) + '1' + ']'.repeat(depth + 2);
    assert.throws(
      () => parseJsonTree(nestedObjects, { maxDepth: depth }),
      /Maximum nesting depth/,
    );
    assert.throws(
      () => parseJsonTree(nestedArrays, { maxDepth: depth }),
      /Maximum nesting depth/,
    );
    assert.equal(({} as any).polluted, undefined);
  });

  it('duplicate keys never reach the merger, so last-wins pollution is impossible', () => {
    assert.throws(
      () => parseJsonTree('{"__proto__":{},"__proto__":{"polluted":"yes"}}'),
      /Duplicate JSON key/,
    );
    assert.throws(
      () => parseJsonTree('{"safe":1,"safe":2}'),
      /Duplicate JSON key/,
    );
  });
});
