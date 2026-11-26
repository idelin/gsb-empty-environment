import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  deriveSecFetchSite,
  evaluateFetchMetadata,
} from '../src/fetch-metadata.ts';

const VICTIM = 'https://victim.example';
const ATTACKER = 'https://attacker.test';
const SUBDOMAIN = 'https://api.victim.example';

test('deriveSecFetchSite classifies origin tiers independently', () => {
  assert.equal(deriveSecFetchSite(VICTIM, VICTIM), 'same-origin');
  assert.equal(deriveSecFetchSite(VICTIM, SUBDOMAIN), 'same-site');
  assert.equal(deriveSecFetchSite(VICTIM, ATTACKER), 'cross-site');
  assert.equal(deriveSecFetchSite(VICTIM, null), 'none');
  assert.equal(deriveSecFetchSite(VICTIM, 'null'), 'none');
});

test('same-origin requests always pass, including state-changing POST', () => {
  const verdict = evaluateFetchMetadata(
    {
      method: 'POST',
      headers: {
        'sec-fetch-site': 'same-origin',
        'sec-fetch-mode': 'cors',
        'sec-fetch-dest': 'empty',
        origin: VICTIM,
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(verdict.allow, true);
  assert.equal(verdict.code, 'allow-same-origin');
});

test('cross-site POST is denied as CSRF', () => {
  const verdict = evaluateFetchMetadata(
    {
      method: 'POST',
      headers: {
        'sec-fetch-site': 'cross-site',
        'sec-fetch-mode': 'cors',
        'sec-fetch-dest': 'empty',
        origin: ATTACKER,
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(verdict.allow, false);
  assert.equal(verdict.code, 'deny-csrf-state-change');
});

test('cross-site GET subresource probes are denied', () => {
  for (const dest of ['image', 'script', 'style', 'font'] as const) {
    const verdict = evaluateFetchMetadata(
      {
        method: 'GET',
        headers: {
          'sec-fetch-site': 'cross-site',
          'sec-fetch-mode': 'no-cors',
          'sec-fetch-dest': dest,
          origin: ATTACKER,
        },
      },
      { serverOrigin: VICTIM },
    );
    assert.equal(verdict.code, 'deny-cross-site-subresource', dest);
  }
});

test('cross-site iframe embedding is denied', () => {
  const verdict = evaluateFetchMetadata(
    {
      method: 'GET',
      headers: {
        'sec-fetch-site': 'cross-site',
        'sec-fetch-mode': 'nested-navigate',
        'sec-fetch-dest': 'iframe',
        origin: ATTACKER,
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(verdict.code, 'deny-cross-site-subresource');
});

test('user-activated top-level navigation is allowed', () => {
  const verdict = evaluateFetchMetadata(
    {
      method: 'GET',
      headers: {
        'sec-fetch-site': 'cross-site',
        'sec-fetch-mode': 'navigate',
        'sec-fetch-dest': 'document',
        'sec-fetch-user': '?1',
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(verdict.allow, true);
  assert.equal(verdict.code, 'allow-user-activation');
});

test('site=none typed URL navigation passes, site=none POST does not', () => {
  const get = evaluateFetchMetadata(
    {
      method: 'GET',
      headers: {
        'sec-fetch-site': 'none',
        'sec-fetch-mode': 'navigate',
        'sec-fetch-dest': 'document',
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(get.code, 'allow-top-level-navigation');

  const post = evaluateFetchMetadata(
    {
      method: 'POST',
      headers: {
        'sec-fetch-site': 'none',
        'sec-fetch-mode': 'navigate',
        'sec-fetch-dest': 'document',
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(post.allow, false);
});

test('Origin: null never inherits trust even with same-origin claim', () => {
  const verdict = evaluateFetchMetadata(
    {
      method: 'GET',
      headers: {
        'sec-fetch-site': 'same-origin',
        'sec-fetch-mode': 'no-cors',
        'sec-fetch-dest': 'iframe',
        origin: 'null',
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(verdict.code, 'deny-null-origin-privilege');
});

test('declared site contradicting the Origin header is rejected', () => {
  const verdict = evaluateFetchMetadata(
    {
      method: 'GET',
      headers: {
        'sec-fetch-site': 'same-origin',
        'sec-fetch-mode': 'cors',
        'sec-fetch-dest': 'empty',
        origin: ATTACKER,
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(verdict.code, 'deny-malformed-metadata');
});

test('cross-site WebSocket handshakes are denied', () => {
  const verdict = evaluateFetchMetadata(
    {
      method: 'GET',
      headers: {
        'sec-fetch-site': 'cross-site',
        'sec-fetch-mode': 'websocket',
        'sec-fetch-dest': 'websocket',
        origin: ATTACKER,
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(verdict.code, 'deny-websocket-cross-site');
});

test('explicit allowlist rules admit scoped cross-site traffic', () => {
  const verdict = evaluateFetchMetadata(
    {
      method: 'GET',
      headers: {
        'sec-fetch-site': 'cross-site',
        'sec-fetch-mode': 'no-cors',
        'sec-fetch-dest': 'font',
        origin: ATTACKER,
      },
    },
    {
      serverOrigin: VICTIM,
      crossSiteAllowlist: [{ dest: 'font', site: 'cross-site' }],
    },
  );
  assert.equal(verdict.allow, true);
  assert.equal(verdict.code, 'allow-explicit-cross-site');
});

test('missing metadata honors the configured posture', () => {
  const permissive = evaluateFetchMetadata(
    { method: 'GET', headers: {} },
    { serverOrigin: VICTIM },
  );
  assert.equal(permissive.code, 'allow-missing-metadata');

  const hardened = evaluateFetchMetadata(
    { method: 'GET', headers: {} },
    { serverOrigin: VICTIM, missingMetadataMode: 'deny' },
  );
  assert.equal(hardened.code, 'deny-missing-metadata');
});

test('same-site subdomain POST without allow rule is still denied', () => {
  const verdict = evaluateFetchMetadata(
    {
      method: 'POST',
      headers: {
        'sec-fetch-site': 'same-site',
        'sec-fetch-mode': 'cors',
        'sec-fetch-dest': 'empty',
        origin: SUBDOMAIN,
      },
    },
    { serverOrigin: VICTIM },
  );
  assert.equal(verdict.code, 'deny-csrf-state-change');
});
