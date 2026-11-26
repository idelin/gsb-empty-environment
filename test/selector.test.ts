import assert from 'node:assert/strict';
import { test } from 'node:test';
import { matchesSelector } from '../src/index.js';

test('empty selector matches everything', () => {
  assert.ok(matchesSelector(undefined, {}));
  assert.ok(matchesSelector({}, { anything: 'x' }));
});

test('matchLabels requires every key/value pair', () => {
  const selector = { matchLabels: { app: 'demo', tier: 'web' } };
  assert.ok(matchesSelector(selector, { app: 'demo', tier: 'web', extra: '1' }));
  assert.ok(!matchesSelector(selector, { app: 'demo' }));
  assert.ok(!matchesSelector(selector, { app: 'demo', tier: 'api' }));
});

test('matchExpressions support In, NotIn, Exists, DoesNotExist and numeric comparisons', () => {
  assert.ok(
    matchesSelector(
      { matchExpressions: [{ key: 'env', operator: 'In', values: ['prod', 'stage'] }] },
      { env: 'stage' },
    ),
  );
  assert.ok(
    matchesSelector(
      { matchExpressions: [{ key: 'env', operator: 'NotIn', values: ['prod'] }] },
      { env: 'dev' },
    ),
  );
  assert.ok(
    !matchesSelector(
      { matchExpressions: [{ key: 'env', operator: 'NotIn', values: ['prod'] }] },
      { env: 'prod' },
    ),
  );
  assert.ok(
    matchesSelector(
      { matchExpressions: [{ key: 'env', operator: 'Exists' }] },
      { env: 'prod' },
    ),
  );
  assert.ok(
    matchesSelector(
      { matchExpressions: [{ key: 'env', operator: 'DoesNotExist' }] },
      {},
    ),
  );
  assert.ok(
    matchesSelector(
      { matchExpressions: [{ key: 'replicas', operator: 'Gt', values: ['3'] }] },
      { replicas: '4' },
    ),
  );
  assert.ok(
    !matchesSelector(
      { matchExpressions: [{ key: 'replicas', operator: 'Lt', values: ['3'] }] },
      { replicas: 'three' },
    ),
  );
});
