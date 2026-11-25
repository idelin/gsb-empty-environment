import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptMerge } from '../src/accept.ts';
import { Authority } from '../src/authority.ts';
import { MergeEngine } from '../src/engine.ts';
import { branch, doc, leaf, obj, ref } from '../src/dsl.ts';
import { CANONICAL_SCENARIOS } from '../src/truth/scenarios.ts';
import type { MergeInput } from '../src/types.ts';

function scenarioInput(id: string): MergeInput {
  const scenario = CANONICAL_SCENARIOS.find((item) => item.id === id);
  assert.ok(scenario, `missing scenario ${id}`);
  return scenario!.input;
}

function runEngine(input: MergeInput) {
  const authority = new Authority(input);
  const engine = new MergeEngine(input, authority);
  return engine.run();
}

test('moved subtree: name/value/existence pass but parent facet is independently denied', () => {
  const input = scenarioInput('moved-subtree-rejects-new-parent');
  const outcome = runEngine(input);
  const toggle = outcome.provenance.find((p) => p.pointer === '/tenants/globex/features/feat/toggle');
  assert.ok(toggle);
  assert.equal(toggle!.name.authorized, true, 'name should be granted');
  assert.equal(toggle!.valueDecision.authorized, true, 'value should be granted and in scope');
  assert.equal(toggle!.existence.authorized, true, 'existence should be granted');
  assert.equal(toggle!.parent.authorized, false, 'parent must be denied at the new tenant');

  const result = acceptMerge(input);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.errors.length, 2, 'exactly one parent error per moved leaf');
    assert.ok(result.errors.every((e) => e.code === 'UNAUTHORIZED_FACET' && e.location.facet === 'parent'));
  }
});

test('same-named leaves under different parents get independent origin and verdict', () => {
  const outcome = runEngine(scenarioInput('same-name-different-parents'));
  const a = outcome.provenance.find((p) => p.pointer === '/a/name');
  const b = outcome.provenance.find((p) => p.pointer === '/b/name');
  assert.ok(a && b);
  assert.equal(a!.valueDecision.originNodeId, 'name-a');
  assert.equal(b!.valueDecision.originNodeId, 'name-b');
  assert.equal(a!.authorized, true);
  assert.equal(b!.authorized, false);
});

test('exposure records revocation state of both deleted and exposed signatures', () => {
  const revoked = runEngine(scenarioInput('delete-exposes-revoked-source'));
  const theme = revoked.provenance.find((p) => p.pointer === '/theme');
  assert.ok(theme);
  assert.equal(theme!.exposures.length, 1);
  assert.equal(theme!.exposures[0].exposedSignatureRevoked, true);
  assert.equal(theme!.exposures[0].deletedSignatureRevoked, false);

  const healthy = runEngine(scenarioInput('delete-then-reinherit'));
  const healthyTheme = healthy.provenance.find((p) => p.pointer === '/theme');
  assert.equal(healthyTheme!.exposures[0].exposedSignatureRevoked, false);
});

test('conditional branches activate from evaluation labels', () => {
  const make = (env: string): MergeInput => ({
    labels: { env },
    branches: [
      { ...branch('prod-only', 20, doc('cp', obj({ k: leaf('prod') }), { attribution: { issuer: 'a' } })), when: { labels: { env: 'prod' } } },
      branch('default', 10, doc('cd', obj({ k: leaf('default') }), { attribution: { issuer: 'a' } })),
    ],
    grants: [{ issuer: '*', path: '/**', facets: '*' }],
    options: { requireSignatureEvidence: false },
  });

  const dev = acceptMerge(make('dev'));
  const prod = acceptMerge(make('prod'));
  assert.ok(dev.ok && prod.ok);
  if (dev.ok) assert.deepEqual(dev.tree, { k: 'default' });
  if (prod.ok) assert.deepEqual(prod.tree, { k: 'prod' });
});

test('alias decision authors the name facet and is traced', () => {
  const input: MergeInput = {
    branches: [
      branch(
        'b',
        10,
        doc('al2', obj({ oldName: leaf(7) }), { attribution: { issuer: 'cfg' } }),
      ),
    ],
    aliases: [{ alias: 'oldName', canonical: 'newName', at: '', attribution: { issuer: 'namer' } }],
    grants: [{ issuer: '*', path: '/**', facets: '*' }],
    options: { requireSignatureEvidence: false },
  };
  const result = acceptMerge(input);
  assert.ok(result.ok, result.ok ? '' : JSON.stringify(result.errors));
  if (result.ok) {
    assert.deepEqual(result.tree, { newName: 7 });
    const prov = result.provenance[0];
    assert.equal(prov.pointer, '/newName');
    assert.equal(prov.name.issuer, 'namer');
    assert.ok(prov.name.via.includes('alias'));
    assert.equal(prov.name.authorized, true);
  }
});

test('unknown template is a located, deterministic failure', () => {
  const input: MergeInput = {
    branches: [branch('b', 1, doc('u', obj({ x: ref('missing') })))],
    grants: [{ issuer: '*', path: '/**', facets: '*' }],
    options: { requireSignatureEvidence: false },
  };
  const result = acceptMerge(input);
  assert.equal(result.ok, false);
  if (!result.ok) assert.ok(result.errors.some((e) => e.code === 'UNKNOWN_TEMPLATE'));
});

test('alias without `at` applies globally, including nested parents', () => {
  const input: MergeInput = {
    branches: [
      branch(
        'b',
        10,
        doc('al3', obj({ svc: obj({ old: leaf(1) }) }), { attribution: { issuer: 'cfg' } }),
      ),
    ],
    aliases: [{ alias: 'old', canonical: 'new', attribution: { issuer: 'namer' } }],
    grants: [{ issuer: '*', path: '/**', facets: '*' }],
    options: { requireSignatureEvidence: false },
  };
  const result = acceptMerge(input);
  assert.ok(result.ok, result.ok ? '' : JSON.stringify(result.errors));
  if (result.ok) {
    assert.deepEqual(result.tree, { svc: { new: 1 } });
    assert.equal(result.provenance[0].pointer, '/svc/new');
    assert.equal(result.provenance[0].name.issuer, 'namer');
  }
});
