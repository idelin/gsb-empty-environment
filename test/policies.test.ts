import { test } from 'node:test';
import assert from 'node:assert/strict';
import { gate } from '../src/bdd.ts';
import {
  CachePublisher,
  FragmentSynthesizer,
  type CspPolicy,
  type FragmentNode,
} from '../src/index.ts';
import { authority, commitmentOf, newBdd, script, source } from './helpers.ts';

function pageWithTwoChains(): FragmentNode {
  const cdn = source('cdn.example');
  const other = source('other.example');
  return {
    id: 'page',
    scripts: [
      script('root-a', '// root a', [cdn]),
      script('root-b', '// root b', [other]),
      script('target', '// target bytes', [cdn, other]),
    ],
    loads: [
      { fromScriptId: 'root-a', slot: 'here', targetScriptId: 'target', source: cdn, gate: gate.varOf('a') },
      { fromScriptId: 'root-b', slot: 'here', targetScriptId: 'target', source: other, gate: gate.varOf('b') },
    ],
    slots: {
      here: {
        inheritSources: [cdn, other],
        fragment: {
          id: 'inline',
          scripts: [
            { id: 'target', bytes: new TextEncoder().encode('// target bytes'), inheritableSources: [cdn, other] },
          ],
          loads: [],
        },
      },
    },
  };
}

test('each policy has a chain but no common execution config exists', () => {
  const bdd = newBdd();
  const policies: CspPolicy[] = [
    {
      id: 'p1',
      strictDynamic: true,
      trustedRoots: [
        { mode: 'nonce', scriptId: 'root-a', gate: gate.varOf('a') },
      ],
    },
    {
      id: 'p2',
      strictDynamic: true,
      trustedRoots: [
        { mode: 'nonce', scriptId: 'root-b', gate: gate.varOf('b') },
      ],
    },
  ];
  const synth = new FragmentSynthesizer({ bdd, authority: authority('resp-1'), policies });
  const graph = synth.instantiate(pageWithTwoChains(), { publishNonces: true });

  const d1 = synth.decide('here::target');
  assert.ok(bdd.isSat(d1.perPolicy.p1));
  assert.ok(bdd.isSat(d1.perPolicy.p2));
  assert.deepEqual(bdd.witness(d1.perPolicy.p1), { a: true });
  assert.deepEqual(bdd.witness(d1.perPolicy.p2), { b: true });

  // Add mutually exclusive gates so the full decisions cannot co-occur.
  const exclusive: CspPolicy[] = [
    { id: 'p1', strictDynamic: true, trustedRoots: [{ mode: 'nonce', scriptId: 'root-a', gate: gate.varOf('a') }] },
    { id: 'p2', strictDynamic: true, trustedRoots: [{ mode: 'nonce', scriptId: 'root-b', gate: gate.and(gate.varOf('b'), gate.not(gate.varOf('a'))) }] },
  ];
  const synth2 = new FragmentSynthesizer({ bdd, authority: authority('resp-2'), policies: exclusive });
  synth2.instantiate(pageWithTwoChains(), { publishNonces: true });
  const decision = synth2.decide('here::target');
  assert.ok(bdd.isSat(decision.perPolicy.p1));
  assert.ok(bdd.isSat(decision.perPolicy.p2));
  assert.equal(decision.allowed, false);
  assert.equal(decision.allowedCondition, bdd.FALSE);
  assert.equal(decision.shortestChain, undefined);

  // Reference: a naive union-of-sources host check would still match target.
  const hosts = new Set<string>();
  for (const s of graph.scripts.values()) {
    for (const src of s.inheritableSources) {
      hosts.add(src.host);
    }
  }
  assert.ok(hosts.has('cdn.example') && hosts.has('other.example'));
});

test('shared fragment reused at two positions gets different inherited sources', () => {
  const bdd = newBdd();
  const cdn = source('cdn.example');
  const allowedHost = source('allowed.example');
  const blockedHost = source('blocked.example');
  const shared: FragmentNode = {
    id: 'shared',
    scripts: [script('widget', '// widget', [cdn])],
    loads: [],
  };
  const page: FragmentNode = {
    id: 'page',
    scripts: [script('root', '// root', [allowedHost, blockedHost])],
    loads: [
      { fromScriptId: 'root', slot: 'ok', targetScriptId: 'widget', source: allowedHost, gate: gate.constOf(true) },
      { fromScriptId: 'root', slot: 'bad', targetScriptId: 'widget', source: allowedHost, gate: gate.constOf(true) },
    ],
    slots: {
      ok: { fragment: shared, inheritSources: [allowedHost] },
      bad: { fragment: shared, inheritSources: [blockedHost] },
    },
  };
  const policies: CspPolicy[] = [
    { id: 'p', strictDynamic: true, trustedRoots: [{ mode: 'nonce', scriptId: 'root', gate: gate.constOf(true) }] },
  ];
  const synth = new FragmentSynthesizer({ bdd, authority: authority('resp-3'), policies });
  synth.instantiate(page, { publishNonces: true });

  const ok = synth.decide('ok::widget');
  const bad = synth.decide('bad::widget');
  assert.equal(ok.allowed, true);
  assert.ok(ok.shortestChain?.some((hop) => hop.positionKey === 'ok::widget'));
  assert.equal(bad.allowed, false);
  assert.ok(bad.denials.some((d) => d.code === 'source-not-inheritable'));

  // Certificate for publishing the shared cache covers only acceptable configs.
  const publisher = new CachePublisher(new TextEncoder().encode('host-key'));
  const configs = synth.renderConfigsFor('ok::widget');
  assert.equal(configs.length, 1);
  const certificate = publisher.publish('cache-1', 'shared', configs);
  const verified = publisher.verify(certificate, {
    fragmentId: 'shared',
    responseId: 'resp-3',
    positionKey: 'ok::widget',
    scriptId: 'widget',
    commitment: commitmentOf('// widget'),
    nonce: configs[0].nonce,
    activeCondition: bdd.TRUE,
    bdd,
  });
  assert.equal(verified.ok, true);

  // Any content-commitment change invalidates the authorization.
  const tampered = publisher.verify(certificate, {
    fragmentId: 'shared',
    responseId: 'resp-3',
    positionKey: 'ok::widget',
    scriptId: 'widget',
    commitment: commitmentOf('// different bytes'),
    nonce: configs[0].nonce,
    activeCondition: bdd.TRUE,
    bdd,
  });
  assert.equal(tampered.ok, false);
  if (!tampered.ok) {
    assert.equal(tampered.reason, 'commitment-changed');
  }
});

test('policies are intersected as full decisions, not by unioning source sets', () => {
  const bdd = newBdd();
  const cdn = source('cdn.example');
  const other = source('other.example');
  // One target script reachable from two roots on different hosts.
  const page: FragmentNode = {
    id: 'page',
    scripts: [
      script('root-cdn', '// cdn root', [cdn]),
      script('root-other', '// other root', [other]),
    ],
    loads: [
      { fromScriptId: 'root-cdn', slot: 't', targetScriptId: 'target', source: cdn, gate: gate.constOf(true) },
      { fromScriptId: 'root-other', slot: 't', targetScriptId: 'target', source: other, gate: gate.constOf(true) },
    ],
    slots: {
      t: {
        inheritSources: [cdn, other],
        fragment: { id: 'tf', scripts: [script('target', '// target', [cdn, other])], loads: [] },
      },
    },
  };
  // Each policy trusts only its own root. A union of the two policies' source
  // sets would cover both hosts and wrongly accept the target.
  const policies: CspPolicy[] = [
    { id: 'cdn-only', strictDynamic: true, trustedRoots: [{ mode: 'nonce', scriptId: 'root-cdn', gate: gate.constOf(true) }] },
    { id: 'other-only', strictDynamic: true, trustedRoots: [{ mode: 'nonce', scriptId: 'root-other', gate: gate.constOf(true) }] },
  ];
  const synth = new FragmentSynthesizer({ bdd, authority: authority('resp-11'), policies });
  synth.instantiate(page, { publishNonces: true });
  const decision = synth.decide('t::target');

  // Each policy individually has a valid chain to the target.
  assert.ok(bdd.isSat(decision.perPolicy['cdn-only']));
  assert.ok(bdd.isSat(decision.perPolicy['other-only']));
  // Their decisions are disjoint root provenances, so the conjunction is empty.
  assert.equal(decision.allowed, false);
  assert.equal(decision.allowedCondition, bdd.FALSE);

  // A third policy whose source-set union would match the target's hosts, yet
  // under strict-dynamic a host-only entry cannot start a trusted chain.
  const hostUnionPolicies: CspPolicy[] = [
    {
      id: 'host-union',
      strictDynamic: true,
      trustedRoots: [
        { mode: 'host', source: cdn, gate: gate.constOf(true) },
        { mode: 'host', source: other, gate: gate.constOf(true) },
      ],
    },
  ];
  const hostSynth = new FragmentSynthesizer({ bdd, authority: authority('resp-12'), policies: hostUnionPolicies });
  hostSynth.instantiate(page, { publishNonces: true });
  const hostDecision = hostSynth.decide('t::target');
  assert.equal(hostDecision.allowed, false);
  assert.ok(hostDecision.denials.every((d) => d.code === 'host-only-under-strict-dynamic'));
  assert.equal(decision.shortestChain, undefined);
});
