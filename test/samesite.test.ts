import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isSameSite, registrableDomain } from '../src/samesite.ts';

test('registrableDomain handles multi-label public suffixes', () => {
  assert.equal(registrableDomain('shop.example.co.uk'), 'example.co.uk');
  assert.equal(registrableDomain('login.example.com'), 'example.com');
  assert.equal(registrableDomain('localhost'), 'localhost');
  assert.equal(registrableDomain('127.0.0.1'), '127.0.0.1');
});

test('isSameSite treats subdomains as same site', () => {
  assert.equal(isSameSite('https://login.example.com', 'https://www.example.com'), true);
  assert.equal(isSameSite('https://a.b.example.com', 'https://example.com'), true);
});

test('isSameSite rejects unrelated domains and scheme mismatches', () => {
  assert.equal(isSameSite('https://login.example.com', 'https://ads.third-party.test'), false);
  assert.equal(isSameSite('https://example.co.uk', 'https://example.com'), false);
  assert.equal(isSameSite('http://www.example.com', 'https://www.example.com'), false);
});

test('isSameSite is invalid for unparseable origins', () => {
  assert.equal(isSameSite('not-a-url', 'https://example.com'), false);
});
