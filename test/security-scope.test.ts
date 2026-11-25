import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptMerge } from '../src/accept.ts';
import { branch, doc, leaf, obj, ref } from '../src/dsl.ts';

test('template expansion does not extend a partial template signature to unsigned leaves', () => {
  const partialTemplate = doc(
    'ptpl',
    obj({ replicas: leaf(1, { id: 'p-rep' }), mode: leaf('static', { id: 'p-mode' }) }, { id: 'p-root' }),
    { rootId: 'p-root', attribution: { issuer: 'tpl', sigId: 's-partial' } },
  );
  const host = doc(
    'phost',
    obj({ web: ref('pt', { id: 'ref-pt', issuer: 'dep', sig: 's-dep' }) }),
    { rootId: 'ph-root' },
  );

  const result = acceptMerge({
    branches: [branch('b', 10, host)],
    templates: { pt: partialTemplate },
    signatures: [
      // Signs only the replicas leaf, NOT the whole template root.
      { sigId: 's-partial', issuer: 'tpl', docId: 'ptpl', nodeId: 'p-rep' },
      { sigId: 's-dep', issuer: 'dep', docId: 'depdoc', nodeId: 'deproot' },
    ],
    grants: [{ issuer: 'dep', path: '/**', facets: '*' }],
  });

  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.ok(
      result.errors.some((e) => e.code === 'SIGNATURE_SCOPE' && e.location.pointer === '/web/mode'),
      'mode leaf must be outside partial template scope',
    );
    assert.ok(
      !result.errors.some((e) => e.code === 'SIGNATURE_SCOPE' && e.location.pointer === '/web/replicas'),
      'replicas leaf is signed and must not be a scope error',
    );
  }
});

test('revoked deployment signature is rejected even with a broad grant', () => {
  const tree = doc('rd', obj({ k: leaf('v') }), {
    rootId: 'rd-root',
    attribution: { issuer: 'a', sigId: 's-a' },
  });
  const result = acceptMerge({
    branches: [branch('b', 1, tree)],
    signatures: [{ sigId: 's-a', issuer: 'a', docId: 'rd', nodeId: 'rd-root', revoked: true }],
    grants: [{ issuer: 'a', path: '/**', facets: '*' }],
  });
  assert.equal(result.ok, false);
  if (!result.ok) assert.ok(result.errors.some((e) => e.code === 'SIGNATURE_REVOKED' && e.location.pointer === '/k'));
});
