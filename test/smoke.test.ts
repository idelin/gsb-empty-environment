import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptMerge } from '../src/accept.ts';
import { branch, doc, leaf, obj } from '../src/dsl.ts';

test('smoke: fully signed and granted single branch publishes', () => {
  const tree = doc(
    'base',
    obj({ db: obj({ host: leaf('b.example') }, { id: 'db' }) }, { id: 'root' }),
    { rootId: 'root', attribution: { issuer: 'acme', sigId: 's1' } },
  );

  const result = acceptMerge({
    branches: [branch('main', 10, tree)],
    signatures: [{ sigId: 's1', issuer: 'acme', docId: 'base', nodeId: 'root' }],
    grants: [{ issuer: 'acme', path: '/**', facets: '*' }],
  });

  if (!result.ok) {
    console.error(JSON.stringify(result.errors, null, 2));
  }
  assert.equal(result.ok, true);
  if (result.ok) {
    assert.deepEqual(result.tree, { db: { host: 'b.example' } });
    assert.equal(result.leaves.length, 1);
    assert.equal(result.provenance[0].authorized, true);
  }
});
