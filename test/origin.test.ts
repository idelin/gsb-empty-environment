import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  getPublicSuffixes,
  isSameOrigin,
  isSameSite,
  opaqueOrigin,
  originMatchesAllowlist,
  originOfUrl,
  parseOrigin,
  relateOrigins,
  serializeOrigin,
  setPublicSuffixes,
} from '../src/origin.ts';

test('normalizes scheme casing, default ports and host case', () => {
  const origin = parseOrigin('HTTPS://Victim.Example:443/path?q=1');
  assert.equal(origin.opaque, false);
  if (!origin.opaque) {
    assert.equal(origin.scheme, 'https');
    assert.equal(origin.host, 'victim.example');
    assert.equal(origin.port, 443);
    assert.equal(serializeOrigin(origin), 'https://victim.example');
  }
});

test('rejects malformed inputs instead of coercing them', () => {
  assert.throws(() => parseOrigin('not-a-url'), TypeError);
  assert.throws(() => parseOrigin('https://'), TypeError);
  assert.throws(() => parseOrigin('https://host:99999'), TypeError);
});

test('maps sandbox / data / about contexts to opaque origins', () => {
  for (const raw of ['null', 'about:blank', 'about:srcdoc', 'data:text/html,x']) {
    const origin = parseOrigin(raw);
    assert.equal(origin.opaque, true, raw);
    assert.equal(serializeOrigin(origin), 'null');
  }
});

test('distinct opaque origins are never same-origin or same-site', () => {
  const first = opaqueOrigin('a');
  const second = opaqueOrigin('b');
  assert.equal(isSameOrigin(first, second), false);
  assert.equal(isSameSite(first, second), false);
  assert.equal(isSameOrigin(first, first), true);
});

test('strict same-origin requires scheme host and port', () => {
  assert.equal(
    isSameOrigin('https://a.example', 'https://a.example:443'),
    true,
  );
  assert.equal(
    isSameOrigin('https://a.example', 'http://a.example'),
    false,
  );
  assert.equal(
    isSameOrigin('https://a.example', 'https://a.example:8443'),
    false,
  );
  assert.equal(
    isSameOrigin('https://a.example', 'https://b.example'),
    false,
  );
});

test('schemeful same-site uses registrable domains', () => {
  assert.equal(
    isSameSite(
      'https://app.victim.example',
      'https://api.victim.example',
    ),
    true,
  );
  assert.equal(
    isSameSite('https://a.example', 'http://a.example'),
    false,
  );
  assert.equal(
    isSameSite('https://evil.github.io', 'https://victim.github.io'),
    false,
  );
  assert.equal(
    isSameSite(
      'https://pages.victim.github.io',
      'https://cdn.victim.github.io',
    ),
    true,
  );
});

test('custom public suffixes are respected', () => {
  const original = getPublicSuffixes();
  setPublicSuffixes([...original, 'example.cdn']);
  assert.equal(
    isSameSite('https://a.example.cdn', 'https://b.example.cdn'),
    false,
  );
  setPublicSuffixes(original);
});

test('relateOrigins returns the CORP relationship tiers', () => {
  assert.equal(
    relateOrigins('https://victim.example', 'https://victim.example/x'),
    'same-origin',
  );
  assert.equal(
    relateOrigins('https://victim.example', 'https://api.victim.example'),
    'same-site',
  );
  assert.equal(
    relateOrigins('https://victim.example', 'https://attacker.test'),
    'cross-site',
  );
});

test('allowlist comparison defeats suffix/prefix lookalike origins', () => {
  const allowlist = ['https://victim.example'];
  assert.equal(
    originMatchesAllowlist('https://victim.example.attacker.test', allowlist),
    false,
  );
  assert.equal(
    originMatchesAllowlist('https://victim.example:443', allowlist),
    true,
  );
  assert.equal(originMatchesAllowlist('null', allowlist), false);
  assert.equal(
    originMatchesAllowlist('https://VICTIM.example', allowlist),
    true,
  );
});

test('originOfUrl strips path and query', () => {
  assert.equal(
    serializeOrigin(originOfUrl('https://host.example:8443/a/b?x=1#f')),
    'https://host.example:8443',
  );
});
