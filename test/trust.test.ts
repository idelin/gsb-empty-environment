import { test } from 'node:test';
import assert from 'node:assert/strict';
import { gate } from '../src/bdd.ts';
import {
  FragmentSynthesizer,
  NonceAuthority,
  type CspPolicy,
  type FragmentNode,
} from '../src/index.ts';
import { authority, commitmentOf, newBdd, script, source } from './helpers.ts';

function cyclicPage(rootGate: ReturnType<typeof gate.constOf>): { page: FragmentNode; policies: CspPolicy[] } {
  const cdn = source('cdn.example');
  const shared: FragmentNode = {
    id: 'shared',
    scripts: [
      script('s1', '// s1', [cdn]),
      script('s2', '// s2', [cdn]),
    ],
    loads: [
      { fromScriptId: 's1', slot: '.', targetScriptId: 's2', source: cdn, gate: gate.constOf(true) },
      { fromScriptId: 's2', slot: '.', targetScriptId: 's1', source: cdn, gate: gate.constOf(true) },
    ],
    slots: {},
  };
  // s1 <-> s2 are edges inside one fragment: the loading graph is cyclic,
  // while the fragment tree stays finite.
  const page: FragmentNode = {
    id: 'page',
    scripts: [script('bootstrap', '// bootstrap', [cdn])],
    loads: [
      { fromScriptId: 'bootstrap', slot: 'entry', targetScriptId: 's1', source: cdn, gate: gate.constOf(true) },
    ],
    slots: { entry: { fragment: shared, inheritSources: [cdn] } },
  };
  const policies: CspPolicy[] = [
    { id: 'p', strictDynamic: true, trustedRoots: [{ mode: 'nonce', scriptId: 'bootstrap', gate: rootGate }] },
  ];
  return { page, policies };
}

test('revoking the trusted root leaves an orphan cycle', () => {
  const bdd = newBdd();
  const active = cyclicPage(gate.varOf('a'));
  const synth = new FragmentSynthesizer({ bdd, authority: authority('resp-4'), policies: active.policies });
  const graph = synth.instantiate(active.page, { publishNonces: true });

  // With the root enabled (a=true), the chain reaches into the cycle.
  const targetKey = 'entry::s1';
  assert.ok(graph.scripts.has(targetKey));
  const allowed = synth.decide(targetKey);
  assert.ok(bdd.isSat(allowed.allowedCondition));
  assert.ok(allowed.shortestChain);
  assert.equal(allowed.shortestChain![0].kind, 'root');

  // Revoke the root: gate forced off. The s1<->s2 cycle remains in the graph
  // but nothing in it can trace back to a trusted root.
  const revoked = cyclicPage(gate.constOf(false));
  const synth2 = new FragmentSynthesizer({ bdd, authority: authority('resp-5'), policies: revoked.policies });
  synth2.instantiate(revoked.page, { publishNonces: true });
  const denied = synth2.decide(targetKey);
  assert.equal(denied.allowed, false);
  assert.equal(denied.allowedCondition, bdd.FALSE);
  assert.ok(denied.denials.some((d) => d.code === 'orphan-cycle-no-root'));
  assert.equal(denied.shortestChain, undefined);
});

test('same host but different script bytes fails hash commitments', () => {
  const bdd = newBdd();
  const cdn = source('cdn.example');
  const page: FragmentNode = {
    id: 'page',
    scripts: [
      script('good', '// good bytes', [cdn]),
      script('evil', '// totally different bytes on the same host', [cdn]),
    ],
    loads: [],
  };
  const policies: CspPolicy[] = [
    {
      id: 'p',
      strictDynamic: true,
      trustedRoots: [{ mode: 'hash', scriptId: 'good', commitment: commitmentOf('// good bytes'), gate: gate.constOf(true) }],
    },
  ];
  const synth = new FragmentSynthesizer({ bdd, authority: authority('resp-6'), policies });
  synth.instantiate(page, { publishNonces: true });

  assert.equal(synth.decide('good').allowed, true);
  const evil = synth.decide('evil');
  assert.equal(evil.allowed, false);
  assert.ok(evil.denials.some((d) => d.code === 'commitment-mismatch'));

  // A host-only root cannot bootstrap trust under strict-dynamic even when
  // the hostname matches.
  const hostOnly: CspPolicy[] = [
    { id: 'p', strictDynamic: true, trustedRoots: [{ mode: 'host', source: cdn, gate: gate.constOf(true) }] },
  ];
  const synth2 = new FragmentSynthesizer({ bdd, authority: authority('resp-7'), policies: hostOnly });
  synth2.instantiate(page, { publishNonces: true });
  const hostDecision = synth2.decide('evil');
  assert.equal(hostDecision.allowed, false);
  assert.ok(hostDecision.denials.some((d) => d.code === 'host-only-under-strict-dynamic'));
});

test('unpublished nonce cancels authorization and old nonces cannot enter a new response', () => {
  const bdd = newBdd();
  const cdn = source('cdn.example');
  const page: FragmentNode = {
    id: 'page',
    scripts: [script('root', '// root', [cdn])],
    loads: [],
  };
  const policies: CspPolicy[] = [
    { id: 'p', strictDynamic: true, trustedRoots: [{ mode: 'nonce', scriptId: 'root', gate: gate.constOf(true) }] },
  ];

  // Nonce minted but never published: authorization is withheld.
  const synth = new FragmentSynthesizer({ bdd, authority: authority('resp-8'), policies });
  synth.instantiate(page);
  const unpublished = synth.decide('root');
  assert.equal(unpublished.allowed, false);
  assert.ok(unpublished.denials.some((d) => d.code === 'nonce-not-published'));

  // A nonce from the previous response must be rejected on the new page.
  const oldAuthority = new NonceAuthority('resp-old');
  const oldNonce = oldAuthority.mint({ positionKey: 'root', scriptId: 'root', commitment: commitmentOf('// root') });
  oldAuthority.publish(oldNonce);
  const newAuthority = new NonceAuthority('resp-9');
  const foreign = newAuthority.verify(oldNonce, {
    responseId: 'resp-9',
    positionKey: 'root',
    scriptId: 'root',
    commitment: commitmentOf('// root'),
  });
  assert.equal(foreign.ok, false);
  if (!foreign.ok) {
    assert.equal(foreign.reason, 'nonce-foreign-response');
  }

  // Publishing the fresh per-response nonce restores authorization.
  const synth2 = new FragmentSynthesizer({ bdd, authority: authority('resp-9'), policies });
  synth2.instantiate(page, { publishNonces: true });
  assert.equal(synth2.decide('root').allowed, true);
});

test('shortest authorization chain is reported with exact hop count', () => {
  const bdd = newBdd();
  const cdn = source('cdn.example');
  const page: FragmentNode = {
    id: 'page',
    scripts: [script('root', '// root', [cdn])],
    loads: [
      { fromScriptId: 'root', slot: 'a', targetScriptId: 'mid', source: cdn, gate: gate.constOf(true) },
    ],
    slots: {
      a: {
        inheritSources: [cdn],
        fragment: {
          id: 'mid-frag',
          scripts: [script('mid', '// mid', [cdn])],
          loads: [
            { fromScriptId: 'mid', slot: 'b', targetScriptId: 'leaf', source: cdn, gate: gate.varOf('c') },
          ],
          slots: {
            b: {
              inheritSources: [cdn],
              fragment: { id: 'leaf-frag', scripts: [script('leaf', '// leaf', [cdn])], loads: [] },
            },
          },
        },
      },
    },
  };
  const policies: CspPolicy[] = [
    { id: 'p', strictDynamic: true, trustedRoots: [{ mode: 'nonce', scriptId: 'root', gate: gate.constOf(true) }] },
  ];
  const synth = new FragmentSynthesizer({ bdd, authority: authority('resp-10'), policies });
  synth.instantiate(page, { publishNonces: true });
  const decision = synth.decide('a/b::leaf');
  assert.equal(decision.allowed, true);
  assert.deepEqual(decision.shortestChain?.map((hop) => hop.kind), ['root', 'load', 'load']);
  assert.deepEqual(decision.shortestChain?.map((hop) => hop.positionKey), ['root', 'a::mid', 'a/b::leaf']);

  // Gate c off: no chain despite the host matching every edge.
  const off = bdd.and(decision.allowedCondition, bdd.not(bdd.literal('c')));
  assert.equal(off, bdd.FALSE);
});
