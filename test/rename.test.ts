import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptMerge } from '../src/accept.ts';
import { branch, doc, leaf, obj } from '../src/dsl.ts';

test('renaming a signed node traces `renamed`; name facet still needs its own grant', () => {
  const original = doc('rn-orig', obj({ oldName: leaf('v', { id: 'rn-leaf' }) }), {
    rootId: 'rn-root',
    attribution: { issuer: 'ren', sigId: 's-rn' },
  });
  const renamed = doc('rn-new', obj({ newName: leaf('v', { id: 'rn-leaf', issuer: 'ren', sig: 's-rn' }) }), {
    rootId: 'rn-new-root',
  });

  const granted = acceptMerge({
    documents: [original],
    branches: [branch('b', 10, renamed)],
    signatures: [{ sigId: 's-rn', issuer: 'ren', docId: 'rn-orig', nodeId: 'rn-root' }],
    grants: [{ issuer: 'ren', path: '/**', facets: '*' }],
  });
  assert.equal(granted.ok, true, granted.ok ? '' : JSON.stringify(granted.errors));
  if (granted.ok) {
    assert.deepEqual(granted.tree, { newName: 'v' });
    const prov = granted.provenance[0];
    assert.equal(prov.pointer, '/newName');
    assert.ok(prov.valueDecision.via.includes('renamed'));
  }

  // Same rename, but the name purpose is not granted -> name facet rejected.
  const nameDenied = acceptMerge({
    documents: [original],
    branches: [branch('b', 10, renamed)],
    signatures: [{ sigId: 's-rn', issuer: 'ren', docId: 'rn-orig', nodeId: 'rn-root' }],
    grants: [{ issuer: 'ren', path: '/**', facets: ['value', 'parent', 'existence'] }],
  });
  assert.equal(nameDenied.ok, false);
  if (!nameDenied.ok) {
    assert.ok(
      nameDenied.errors.some(
        (e) => e.code === 'UNAUTHORIZED_FACET' && e.location.facet === 'name' && e.location.pointer === '/newName',
      ),
    );
  }
});
