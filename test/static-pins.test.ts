import assert from 'node:assert/strict';
import test from 'node:test';

import { StaticPinValidator } from '../index.ts';

test('static pins match any certificate in the chain', () => {
  const validator = new StaticPinValidator({
    staticPins: {
      'example.com': ['sha256/root-backup-pin'],
    },
  });

  const result = validator.verify('example.com', [
    { spkiSha256: 'sha256/leaf-pin' },
    { spkiSha256: 'sha256/backup-pin' },
    { spkiSha256: 'sha256/root-backup-pin' },
  ]);

  assert.equal(result.allowed, true);
  assert.equal(result.enforced, true);
  assert.equal(result.matchedPin, 'root-backup-pin');
});

test('user-installed roots are not exempt by default', () => {
  const validator = new StaticPinValidator({
    staticPins: {
      'example.com': ['sha256/expected-root-pin'],
    },
  });

  const chain = [
    { spkiSha256: 'sha256/leaf-pin' },
    { spkiSha256: 'sha256/local-root-pin', userInstalledRoot: true },
  ];

  const byDefault = validator.verify('example.com', chain);
  assert.equal(byDefault.allowed, false);
  assert.equal(byDefault.enforced, true);
  assert.equal(byDefault.reason, 'pin-mismatch');

  const exempted = validator.verify('example.com', chain, {
    exemptUserInstalledRoots: true,
  });
  assert.equal(exempted.allowed, true);
  assert.equal(exempted.enforced, false);
  assert.equal(exempted.reason, 'user-installed-root-exempt');
});

test('config can exempt user-installed roots', () => {
  const validator = new StaticPinValidator({
    staticPins: {
      'example.com': ['sha256/expected-root-pin'],
    },
    exemptUserInstalledRoots: true,
  });

  const userRoot = validator.verify('example.com', [
    { spkiSha256: 'sha256/leaf-pin' },
    {
      spkiSha256: 'sha256/local-root-pin',
      isUserInstalledRoot: true,
    },
  ]);
  assert.equal(userRoot.allowed, true);
  assert.equal(userRoot.reason, 'user-installed-root-exempt');

  const publicRoot = validator.verify('example.com', [
    { spkiSha256: 'sha256/leaf-pin' },
    { spkiSha256: 'sha256/public-root-pin' },
  ]);
  assert.equal(publicRoot.allowed, false);
  assert.equal(publicRoot.reason, 'pin-mismatch');
});

test('an empty certificate chain cannot satisfy a static pin', () => {
  const validator = new StaticPinValidator({
    staticPins: { 'example.com': ['sha256/leaf-pin'] },
  });
  const result = validator.verify('example.com', []);
  assert.equal(result.allowed, false);
  assert.equal(result.reason, 'empty-chain');
});

test('host matching accepts URLs, ports, and a trailing dot', () => {
  const validator = new StaticPinValidator({
    staticPins: {
      'https://example.com:443': ['sha256/leaf-pin'],
    },
  });

  const result = validator.verify(
    'https://EXAMPLE.com/path?query=1',
    [{ spkiSha256: 'sha256/leaf-pin' }],
  );
  assert.equal(result.allowed, true);
  assert.equal(result.host, 'example.com');

  const trailingDot = validator.verify('example.com.', [
    { spkiSha256: 'sha256/leaf-pin' },
  ]);
  assert.equal(trailingDot.allowed, true);
});
