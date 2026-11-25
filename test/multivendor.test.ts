import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptMerge } from '../src/accept.ts';
import { branch, doc, leaf, obj, tomb } from '../src/dsl.ts';

test('two vendors sign disjoint subtrees of the same original tree', () => {
  const shared = doc(
    'shared',
    obj({
      payments: obj(
        {
          secret: leaf('ps', { id: 'pay-secret', issuer: 'payvendor', sig: 's-pay' }),
          endpoint: leaf('https://pay', { id: 'pay-ep', issuer: 'payvendor', sig: 's-pay' }),
        },
        { id: 'pay-root' },
      ),
      search: obj(
        { endpoint: leaf('https://search', { id: 'srch-ep', issuer: 'searchvendor', sig: 's-srch' }) },
        { id: 'srch-root' },
      ),
    }),
    { rootId: 'shared-root' },
  );

  const result = acceptMerge({
    documents: [shared],
    branches: [branch('b', 10, shared)],
    signatures: [
      { sigId: 's-pay', issuer: 'payvendor', docId: 'shared', nodeId: 'pay-root' },
      { sigId: 's-srch', issuer: 'searchvendor', docId: 'shared', nodeId: 'srch-root' },
    ],
    grants: [
      { issuer: 'payvendor', path: '/payments/**', facets: '*' },
      { issuer: 'searchvendor', path: '/search/**', facets: '*' },
    ],
  });

  if (!result.ok) console.error(JSON.stringify(result.errors, null, 2));
  assert.equal(result.ok, true);
  if (result.ok) {
    assert.equal(result.leaves.length, 3);
    assert.ok(result.provenance.every((p) => p.authorized));
  }
});

test('a HIGH-priority cross-scope tombstone is also blocked, not applied', () => {
  const secure = doc(
    'sv',
    obj({ k: leaf('protected', { id: 'k-secure', issuer: 'secure', sig: 's-sec' }) }),
    { rootId: 'sv-root' },
  );
  const ops = doc(
    'ov',
    obj({ k: tomb({ id: 'k-tomb', issuer: 'ops', sig: 's-ops' }) }),
    { rootId: 'ov-root' },
  );

  const result = acceptMerge({
    branches: [
      branch('ops-high', 90, ops),
      branch('secure-low', 10, secure),
    ],
    signatures: [
      { sigId: 's-sec', issuer: 'secure', docId: 'sv', nodeId: 'sv-root' },
      { sigId: 's-ops', issuer: 'ops', docId: 'ov', nodeId: 'ov-root' },
    ],
    grants: [
      { issuer: 'secure', path: '/**', facets: '*' },
      { issuer: 'ops', path: '/**', facets: '*' },
    ],
    protectedKeys: [{ path: '/k', owner: 'secure' }],
  });

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.ok(result.errors.some((e) => e.code === 'CROSS_SCOPE_TOMBSTONE' && e.location.pointer === '/k'));
  }
});

test('delegated cross-scope deletion is accepted', () => {
  const secure = doc(
    'sv2',
    obj({ k: leaf('protected', { id: 'k2', issuer: 'secure', sig: 's-sec2' }) }),
    { rootId: 'sv2-root' },
  );
  const ops = doc(
    'ov2',
    obj({ k: tomb({ id: 'kt2', issuer: 'ops', sig: 's-ops2' }) }),
    { rootId: 'ov2-root' },
  );

  const result = acceptMerge({
    branches: [branch('ops', 90, ops), branch('secure', 10, secure)],
    signatures: [
      { sigId: 's-sec2', issuer: 'secure', docId: 'sv2', nodeId: 'sv2-root' },
      { sigId: 's-ops2', issuer: 'ops', docId: 'ov2', nodeId: 'ov2-root' },
    ],
    grants: [{ issuer: 'ops', path: '/**', facets: '*' }],
    protectedKeys: [{ path: '/k', owner: 'secure', allowDeleteFrom: ['ops'] }],
  });

  assert.equal(result.ok, true, result.ok ? '' : JSON.stringify(result.errors, null, 2));
  if (result.ok) assert.deepEqual(result.tree, {});
});
