import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isSameSite, registrableDomain } from '../src/sameSite.ts';

test('registrableDomain handles common and multi-part suffixes', () => {
  assert.equal(registrableDomain('https://www.example.co.uk/p'), 'example.co.uk');
  assert.equal(registrableDomain('https://auth.example.com/p'), 'example.com');
  assert.equal(registrableDomain('https://login.example.com:8443/'), 'example.com');
  assert.equal(registrableDomain('http://localhost:3000/'), 'localhost');
  assert.equal(registrableDomain('https://127.0.0.1:8080/'), '127.0.0.1');
  assert.equal(registrableDomain('not a url'), null);
});

test('same-site subdomains match regardless of port or path', () => {
  assert.equal(
    isSameSite('https://auth.example.com/login', 'https://www.example.com/'),
    true,
  );
  assert.equal(
    isSameSite('https://auth.example.com:9000', 'https://example.com'),
    true,
  );
});

test('cross-site, scheme mismatch and opaque origins fail', () => {
  assert.equal(
    isSameSite('https://ads.third-party.test', 'https://login.example.com'),
    false,
  );
  assert.equal(
    isSameSite('http://example.com', 'https://example.com'),
    false,
  );
  assert.equal(isSameSite('null', 'https://example.com'), false);
  assert.equal(isSameSite('garbage', 'https://example.com'), false);
});

test('multi-part suffixes do not collapse distinct registrable domains', () => {
  assert.equal(
    isSameSite('https://a.example.co.uk', 'https://b.example.co.uk'),
    true,
  );
  assert.equal(
    isSameSite('https://attackerco.uk', 'https://victim.example.co.uk'),
    false,
  );
});
