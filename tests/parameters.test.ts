import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { ParameterStore } from '../src/parameters.ts';
import { ParameterObject, ParamRef } from '../src/types.ts';

const param = (
  namespace: string,
  name: string,
  labels: Record<string, string>,
): ParameterObject => ({
  apiVersion: 'policy.example.com/v1',
  kind: 'LimitConfig',
  metadata: { namespace, name, labels },
  maxReplicas: 3,
});

describe('ParameterStore resolution', () => {
  test('fixed name resolves a single object in the given namespace', () => {
    const store = new ParameterStore();
    store.upsert(param('team-a', 'cfg', { tier: 'gold' }));
    const ref: ParamRef = { name: 'cfg', namespace: 'team-a' };
    const result = store.resolve(ref, { apiVersion: 'policy.example.com/v1', kind: 'LimitConfig' });
    assert.equal(result.found, true);
    assert.equal(result.objects.length, 1);
    assert.deepEqual(result.objects[0].key, { namespace: 'team-a', name: 'cfg' });
  });

  test('label selector expands to every matching parameter object', () => {
    const store = new ParameterStore();
    store.upsert(param('team-a', 'cfg-1', { tier: 'gold' }));
    store.upsert(param('team-a', 'cfg-2', { tier: 'gold' }));
    store.upsert(param('team-a', 'cfg-3', { tier: 'silver' }));
    const ref: ParamRef = {
      namespace: 'team-a',
      selector: { matchLabels: { tier: 'gold' } },
    };
    const result = store.resolve(ref, { kind: 'LimitConfig' });
    assert.equal(result.found, true);
    assert.deepEqual(
      result.objects.map((entry) => entry.key.name),
      ['cfg-1', 'cfg-2'],
    );
  });

  test('selector without namespace searches cluster-wide', () => {
    const store = new ParameterStore();
    store.upsert(param('team-a', 'cfg', { shared: 'true' }));
    store.upsert(param('team-b', 'cfg', { shared: 'true' }));
    store.upsert(param('team-b', 'other', { shared: 'false' }));
    const ref: ParamRef = { selector: { matchLabels: { shared: 'true' } } };
    const result = store.resolve(ref, undefined);
    assert.equal(result.objects.length, 2);
  });

  test('paramKind apiVersion/kind filters out foreign objects', () => {
    const store = new ParameterStore();
    store.upsert(param('ns', 'cfg', {}));
    const ref: ParamRef = { name: 'cfg', namespace: 'ns' };
    const wrongKind = store.resolve(ref, { kind: 'OtherKind' });
    assert.equal(wrongKind.found, false);
  });

  test('missing object reports not found', () => {
    const store = new ParameterStore();
    const result = store.resolve(
      { name: 'ghost', namespace: 'ns' },
      undefined,
    );
    assert.equal(result.found, false);
    assert.deepEqual(result.objects, []);
  });
});
