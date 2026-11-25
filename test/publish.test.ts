import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptMerge, publishAccepted } from '../src/index.ts';
import { branch, doc, leaf, obj, tomb } from '../src/index.ts';
import type { PublishHost, Publication } from '../src/index.ts';

function recordingHost(): PublishHost & { calls: Publication[] } {
  const calls: Publication[] = [];
  return { calls, publish: (p) => void calls.push(p) };
}

test('publishAccepted calls the host exactly once on acceptance', async () => {
  const tree = doc('d', obj({ k: leaf('v') }), { attribution: { issuer: 'a' } });
  const host = recordingHost();
  const result = await publishAccepted(host, {
    branches: [branch('b', 1, tree)],
    grants: [{ issuer: '*', path: '/**', facets: '*' }],
    options: { requireSignatureEvidence: false },
  });
  assert.equal(result.ok, true);
  assert.equal(host.calls.length, 1);
  assert.match(host.calls[0].publicationId, /^pub_[0-9a-f]{8}$/);
});

test('failure means zero publication and no host invocation', async () => {
  const tree = doc('d2', obj({ k: tomb({ id: 't' }) }), {
    rootId: 'r',
    attribution: { issuer: 'ops', sigId: 's' },
  });
  const host = recordingHost();
  const result = await publishAccepted(host, {
    branches: [branch('b', 10, tree)],
    signatures: [{ sigId: 's', issuer: 'ops', docId: 'd2', nodeId: 'r' }],
    protectedKeys: [{ path: '/k', owner: 'secure' }],
    grants: [{ issuer: 'ops', path: '/**', facets: '*' }],
  });
  assert.equal(result.ok, false);
  assert.equal(host.calls.length, 0, 'host must never be called on rejection');
  if (!result.ok) assert.ok(result.errors.some((e) => e.code === 'CROSS_SCOPE_TOMBSTONE'));
});

test('acceptMerge is deterministic: identical error ordering across runs', () => {
  const input = {
    branches: [
      branch(
        'b',
        1,
        doc('d3', obj({ z: leaf(1, { id: 'z' }), a: leaf(2, { id: 'a' }) }), {
          attribution: { issuer: 'nobody' },
        }),
      ),
    ],
  };
  const first = acceptMerge(input);
  const second = acceptMerge(input);
  assert.equal(first.ok, false);
  assert.deepEqual(JSON.stringify(first), JSON.stringify(second));
  if (!first.ok) {
    const keys = first.errors.map((e) => `${e.location.pointer}|${e.location.facet ?? ''}|${e.code}`);
    const sorted = [...keys].sort();
    assert.deepEqual(keys, sorted);
  }
});
