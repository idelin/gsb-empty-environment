import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  hostMatchesPattern,
  isAllowedOrigin,
  originToHost,
  parsePattern,
} from '../src/origin-policy.ts';
import { containsProtoKey, validateOptions } from '../src/options-guard.ts';

test('wildcard pattern matches sub-domains only', () => {
  const pattern = parsePattern('*.example.com');

  assert.equal(hostMatchesPattern('app.example.com', pattern), true);
  assert.equal(hostMatchesPattern('a.b.example.com', pattern), true);

  // apex domain is not a sub-domain
  assert.equal(hostMatchesPattern('example.com', pattern), false);
  // suffix attack: different left-most label
  assert.equal(hostMatchesPattern('evilexample.com', pattern), false);
  // suffix attack: allowed string is not the host tail
  assert.equal(hostMatchesPattern('example.com.evil.net', pattern), false);
  // empty label / stray dot
  assert.equal(hostMatchesPattern('.example.com', pattern), false);
});

test('exact host matching ignores scheme but requires the full host', () => {
  const exact = parsePattern('app.example.com');
  assert.equal(hostMatchesPattern('app.example.com', exact), true);
  assert.equal(hostMatchesPattern('evilapp.example.com', exact), false);
  assert.equal(hostMatchesPattern('notapp.example.com', exact), false);
});

test('isAllowedOrigin normalizes real origins', () => {
  const patterns = [parsePattern('*.example.com'), parsePattern('trusted.example.org')];

  assert.equal(isAllowedOrigin('https://app.example.com', patterns), true);
  assert.equal(isAllowedOrigin('https://example.com', patterns), false);
  assert.equal(isAllowedOrigin('https://evilexample.com', patterns), false);
  assert.equal(isAllowedOrigin('https://example.com.evil.net', patterns), false);
  assert.equal(isAllowedOrigin('http://trusted.example.org', patterns), true);
  assert.equal(isAllowedOrigin('https://trusted.example.org.evil.net', patterns), false);
  assert.equal(isAllowedOrigin('not a url', patterns), false);
  assert.equal(isAllowedOrigin('null', patterns), false);
});

test('host matching ignores port and path but never crosses label boundaries', () => {
  const wildcard = parsePattern('*.example.com');
  const exact = parsePattern('trusted.example.org');

  assert.equal(isAllowedOrigin('https://app.example.com:8443/path', [wildcard]), true);
  assert.equal(isAllowedOrigin('http://trusted.example.org:8080', [exact]), true);
  assert.equal(isAllowedOrigin('https://trusted.example.org.evil.net', [exact]), false);
  assert.equal(isAllowedOrigin('https://wwwtrusted.example.org', [exact]), false);
});

test('originToHost rejects unparseable and opaque origins', () => {
  assert.equal(originToHost('https://a.example.com:8443/path?q=1'), 'a.example.com');
  assert.equal(originToHost('https://APP.Example.COM/'), 'app.example.com');
  assert.equal(originToHost('javascript:alert(1)'), null);
  assert.equal(originToHost(''), null);
});

test('pattern parsing rejects misplaced wildcards and non-origin input', () => {
  assert.throws(() => parsePattern('a.*.com'));
  assert.throws(() => parsePattern('example.*'));
  assert.throws(() => parsePattern('*.com/path'));
  assert.throws(() => parsePattern('https://user:pass@app.example.com'));
  assert.throws(() => parsePattern(''));
});

test('containsProtoKey flags nested __proto__ keys', () => {
  // JSON payloads (the postMessage path) materialize __proto__ as an own key.
  assert.equal(containsProtoKey(JSON.parse('{"__proto__":{"polluted":true}}')), true);
  assert.equal(containsProtoKey({ nested: JSON.parse('{"__proto__":{"x":1}}') }), true);
  const arrayProto: Record<string, number> = {};
  Object.defineProperty(arrayProto, '__proto__', {
    value: 1,
    enumerable: true,
    configurable: true,
    writable: true,
  });
  assert.equal(containsProtoKey({ arr: [arrayProto] }), true);
  assert.equal(containsProtoKey({ safe: 1, deeper: { ok: [1, 2, 3] } }), false);
  assert.equal(containsProtoKey({ constructor: 1 }), false);
  assert.equal(containsProtoKey(undefined), false);
});

test('containsProtoKey survives cyclic objects without throwing', () => {
  const cyclic: Record<string, unknown> = { safe: 1 };
  cyclic.self = cyclic;
  assert.equal(containsProtoKey(cyclic), false);

  const withProto: Record<string, unknown> = { self: cyclic };
  const bad: Record<string, unknown> = {};
  Object.defineProperty(bad, '__proto__', {
    value: cyclic,
    enumerable: true,
    configurable: true,
    writable: true,
  });
  withProto.bad = bad;
  assert.equal(containsProtoKey(withProto), true);
});

test('validateOptions rejects proto payloads and non-plain values', () => {
  assert.deepEqual(validateOptions(undefined), { ok: true });
  assert.deepEqual(validateOptions({}), { ok: true });
  assert.equal(validateOptions(JSON.parse('{"__proto__":{}}')).ok, false);
  assert.equal(validateOptions(new Date()).ok, false);
  assert.equal(validateOptions('string').ok, false);
});

test('proto-key payload never mutates Object.prototype', () => {
  const payload = JSON.parse('{"__proto__":{"polluted":"yes"}}');
  const result = validateOptions(payload);
  assert.equal(result.ok, false);
  assert.equal((Object.prototype as Record<string, unknown>).polluted, undefined);
});
