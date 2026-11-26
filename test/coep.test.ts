import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  corpAllows,
  evaluateCrossOriginIsolated,
  evaluateFrameEmbed,
  evaluateRedirectChain,
  evaluateSubresource,
  parseCoep,
  parseCorp,
} from '../src/coep.ts';
import { parseCoop, parseSandbox } from '../src/coop.ts';
import { opaqueOrigin } from '../src/origin.ts';

const VICTIM = 'https://victim.example';
const ATTACKER = 'https://attacker.test';
const SUBDOMAIN = 'https://cdn.victim.example';

function coep(value: string | null) {
  return parseCoep({ 'cross-origin-embedder-policy': value });
}

test('parser falls back to unsafe-none on missing/unknown values', () => {
  assert.equal(parseCoep({}).value, 'unsafe-none');
  assert.equal(coep('nonsense').value, 'unsafe-none');
  assert.equal(coep('require-corp').value, 'require-corp');
  assert.equal(coep('credentialless').value, 'credentialless');
  assert.equal(parseCorp('same-site').value, 'same-site');
  assert.equal(parseCorp(null).value, null);
  assert.equal(parseCorp('garbage').value, null);
});

test('CORP tier matrix for no-cors responses', () => {
  assert.equal(corpAllows(parseCorp('same-origin'), 'same-origin', 'no-cors'), true);
  assert.equal(corpAllows(parseCorp('same-origin'), 'same-site', 'no-cors'), false);
  assert.equal(corpAllows(parseCorp('same-site'), 'same-site', 'no-cors'), true);
  assert.equal(corpAllows(parseCorp('same-site'), 'cross-site', 'no-cors'), false);
  assert.equal(corpAllows(parseCorp('cross-origin'), 'cross-site', 'no-cors'), true);
  assert.equal(corpAllows(parseCorp(null), 'cross-site', 'no-cors'), false);
  // CORS mode bypasses CORP because the CORS check governs readability.
  assert.equal(corpAllows(parseCorp('same-origin'), 'cross-site', 'cors'), true);
});

test('require-corp blocks cross-origin no-cors subresource without CORP', () => {
  const verdict = evaluateSubresource(
    {
      embedderOrigin: VICTIM,
      requestOrigin: ATTACKER,
      mode: 'no-cors',
      destination: 'script',
      hops: [{ url: ATTACKER }],
    },
    coep('require-corp'),
  );
  assert.equal(verdict.allowed, false);
  assert.equal(verdict.reason, 'corp-missing');
});

test('require-corp honors matching CORP headers across all tiers', () => {
  for (const [origin, corp] of [
    [VICTIM, null],
    [SUBDOMAIN, 'same-site'],
    [ATTACKER, 'cross-origin'],
  ] as const) {
    const verdict = evaluateSubresource(
      {
        embedderOrigin: VICTIM,
        requestOrigin: origin,
        mode: 'no-cors',
        destination: 'image',
        hops: [{ url: origin, corp }],
      },
      coep('require-corp'),
    );
    assert.equal(verdict.allowed, true, `${origin} ${corp}`);
  }
});

test('require-corp rejects overly restrictive CORP', () => {
  const verdict = evaluateSubresource(
    {
      embedderOrigin: VICTIM,
      requestOrigin: ATTACKER,
      mode: 'no-cors',
      destination: 'script',
      hops: [{ url: ATTACKER, corp: 'same-origin' }],
    },
    coep('require-corp'),
  );
  assert.equal(verdict.allowed, false);
  assert.equal(verdict.reason, 'corp-too-restrictive');
});

test('require-corp needs anonymous CORS headers for cors-mode subresources', () => {
  const blocked = evaluateSubresource(
    {
      embedderOrigin: VICTIM,
      requestOrigin: ATTACKER,
      mode: 'cors',
      destination: 'font',
      hops: [{ url: ATTACKER }],
    },
    coep('require-corp'),
  );
  assert.equal(blocked.allowed, false);
  assert.equal(blocked.reason, 'cors-not-allowed');

  const wildcard = evaluateSubresource(
    {
      embedderOrigin: VICTIM,
      requestOrigin: ATTACKER,
      mode: 'cors',
      destination: 'font',
      hops: [{ url: ATTACKER, 'access-control-allow-origin': '*' }],
    },
    coep('require-corp'),
  );
  assert.equal(wildcard.allowed, true);

  const reflected = evaluateSubresource(
    {
      embedderOrigin: VICTIM,
      requestOrigin: ATTACKER,
      mode: 'cors',
      destination: 'font',
      hops: [
        {
          url: ATTACKER,
          'access-control-allow-origin': VICTIM,
        },
      ],
    },
    coep('require-corp'),
  );
  assert.equal(reflected.allowed, true);
});

test('credentialless strips credentials and exempts CORP-less media', () => {
  const verdict = evaluateSubresource(
    {
      embedderOrigin: VICTIM,
      requestOrigin: ATTACKER,
      mode: 'no-cors',
      destination: 'image',
      credentials: 'include',
      hops: [{ url: ATTACKER }],
    },
    coep('credentialless'),
  );
  assert.equal(verdict.allowed, true);
  assert.equal(verdict.effectiveCredentials, 'omit');

  const sameOrigin = evaluateSubresource(
    {
      embedderOrigin: VICTIM,
      requestOrigin: VICTIM,
      mode: 'no-cors',
      destination: 'image',
      credentials: 'include',
      hops: [{ url: VICTIM }],
    },
    coep('credentialless'),
  );
  assert.equal(sameOrigin.effectiveCredentials, 'include');
});

test('redirect chains are evaluated hop by hop and identify the failing hop', () => {
  const verdict = evaluateSubresource(
    {
      embedderOrigin: VICTIM,
      requestOrigin: ATTACKER,
      mode: 'no-cors',
      destination: 'script',
      hops: [
        { url: ATTACKER, corp: 'cross-origin' },
        { url: 'https://leak.other.test' },
      ],
    },
    coep('require-corp'),
  );
  assert.equal(verdict.allowed, false);
  assert.equal(verdict.failedHop, 1);
  assert.equal(verdict.reason, 'corp-missing');
});

test('opaque redirect hops are refused unless anonymous CORS validates them', () => {
  const blocked = evaluateRedirectChain(
    {
      embedderOrigin: VICTIM,
      requestOrigin: ATTACKER,
      mode: 'no-cors',
      destination: 'script',
      hops: [
        { url: ATTACKER, corp: 'cross-origin' },
        { url: opaqueOrigin('sandbox:frame') },
      ],
    },
    coep('require-corp'),
  );
  assert.equal(blocked.allowed, false);
  assert.equal(blocked.reason, 'opaque-origin-redirect');
  assert.equal(blocked.failedHop, 1);

  const allowed = evaluateRedirectChain(
    {
      embedderOrigin: VICTIM,
      requestOrigin: ATTACKER,
      mode: 'cors',
      destination: 'document',
      hops: [
        {
          url: opaqueOrigin('sandbox:frame'),
          'access-control-allow-origin': VICTIM,
        },
      ],
    },
    coep('require-corp'),
  );
  assert.equal(allowed.allowed, true);
});

test('frame embedding: require-corp parent rules', () => {
  const blockedNoCoep = evaluateFrameEmbed({
    parentOrigin: VICTIM,
    parentCoep: coep('require-corp'),
    childOrigin: ATTACKER,
    childCoep: coep(null),
    childCorp: 'cross-origin',
  });
  assert.equal(blockedNoCoep.allowed, false);
  assert.equal(blockedNoCoep.reason, 'coep-iframe-not-embeddable');

  const blockedNoCorp = evaluateFrameEmbed({
    parentOrigin: VICTIM,
    parentCoep: coep('require-corp'),
    childOrigin: ATTACKER,
    childCoep: coep('require-corp'),
  });
  assert.equal(blockedNoCorp.allowed, false);

  const isolated = evaluateFrameEmbed({
    parentOrigin: VICTIM,
    parentCoep: coep('require-corp'),
    childOrigin: ATTACKER,
    childCoep: coep('require-corp'),
    childCorp: 'cross-origin',
  });
  assert.equal(isolated.allowed, true);
  assert.equal(isolated.childCrossOriginIsolated, true);
});

test('frame embedding: credentialless parent anonymous iframe rule', () => {
  const verdict = evaluateFrameEmbed({
    parentOrigin: VICTIM,
    parentCoep: coep('credentialless'),
    childOrigin: ATTACKER,
    childCoep: coep(null),
  });
  assert.equal(verdict.allowed, true);
  assert.equal(verdict.credentiallessEmbedded, true);
  assert.equal(verdict.childCrossOriginIsolated, false);

  const optedIn = evaluateFrameEmbed({
    parentOrigin: VICTIM,
    parentCoep: coep('credentialless'),
    childOrigin: ATTACKER,
    childCoep: coep('require-corp'),
    childCorp: 'cross-origin',
  });
  assert.equal(optedIn.childCrossOriginIsolated, true);
});

test('sandboxed iframe without allow-same-origin becomes an opaque child', () => {
  const blocked = evaluateFrameEmbed({
    parentOrigin: VICTIM,
    parentCoep: coep('require-corp'),
    childOrigin: VICTIM,
    childCoep: coep('require-corp'),
    childSandbox: parseSandbox('allow-scripts'),
  });
  assert.equal(blocked.allowed, false);
  assert.equal(blocked.reason, 'coep-iframe-not-embeddable');

  const verdict = evaluateFrameEmbed({
    parentOrigin: VICTIM,
    parentCoep: coep('require-corp'),
    childOrigin: VICTIM,
    childCoep: coep('require-corp'),
    childCorp: 'cross-origin',
    childSandbox: parseSandbox('allow-scripts'),
  });
  assert.equal(verdict.allowed, true);
  // The opaque sandboxed child still carries COEP require-corp, so it joins
  // the isolated cluster even though its origin is unique.
  assert.equal(verdict.childCrossOriginIsolated, true);
});

test('crossOriginIsolated top-level and nested rules', () => {
  const full = evaluateCrossOriginIsolated({
    origin: VICTIM,
    coop: parseCoop({ 'cross-origin-opener-policy': 'same-origin' }),
    coep: coep('require-corp'),
    isTopLevel: true,
  });
  assert.equal(full.crossOriginIsolated, true);

  const credentialless = evaluateCrossOriginIsolated({
    origin: VICTIM,
    coop: parseCoop({ 'cross-origin-opener-policy': 'same-origin' }),
    coep: coep('credentialless'),
    isTopLevel: true,
  });
  assert.equal(credentialless.crossOriginIsolated, true);

  const weakCoop = evaluateCrossOriginIsolated({
    origin: VICTIM,
    coop: parseCoop({
      'cross-origin-opener-policy': 'same-origin-allow-popups',
    }),
    coep: coep('require-corp'),
    isTopLevel: true,
  });
  assert.equal(weakCoop.crossOriginIsolated, false);

  const nested = evaluateCrossOriginIsolated({
    origin: ATTACKER,
    coop: parseCoop({ 'cross-origin-opener-policy': 'same-origin' }),
    coep: coep('require-corp'),
    isTopLevel: false,
    parentIsolated: true,
  });
  assert.equal(nested.crossOriginIsolated, true);

  const nestedBroken = evaluateCrossOriginIsolated({
    origin: ATTACKER,
    coop: parseCoop({ 'cross-origin-opener-policy': 'same-origin' }),
    coep: coep(null),
    isTopLevel: false,
    parentIsolated: true,
  });
  assert.equal(nestedBroken.crossOriginIsolated, false);
});
