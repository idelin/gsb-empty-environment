import assert from 'node:assert/strict';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  LegacyPinStore,
  NetworkSecurityPolicy,
} from '../index.ts';
import { createTempDirectory } from './helpers.ts';

test('HPKP and Expect-CT change no state and send no reports', () => {
  const reports: unknown[] = [];
  const policy = new NetworkSecurityPolicy({
    reportSender: (report) => {
      reports.push(report);
    },
    now: () => 1_000,
  });

  const result = policy.processResponse('https://example.com/', {
    'Strict-Transport-Security':
      'max-age=31536000; includeSubDomains; preload',
    'Public-Key-Pins':
      'pin-sha256="sha256/attacker-pin"; max-age=2592000; includeSubDomains; report-uri="https://attacker.example/hpkp"',
    'Public-Key-Pins-Report-Only':
      'pin-sha256="sha256/report-only-pin"; report-uri="https://attacker.example/hpkp-only"',
    'Expect-CT':
      'max-age=86400; enforce; report-uri="https://attacker.example/expect-ct"',
  });

  assert.deepEqual(result.ignoredHeaders, [
    'public-key-pins',
    'public-key-pins-report-only',
    'expect-ct',
  ]);
  assert.equal(policy.getDynamicPins('example.com').length, 0);
  assert.equal(policy.getExpectCtState('example.com'), undefined);
  assert.equal(policy.isExpectCtEnforced('example.com'), false);
  assert.equal(policy.getReportRequests().length, 0);
  assert.equal(result.reportRequests.length, 0);
  assert.equal(reports.length, 0);

  const attackerPinCheck = policy.verifyCertificateChain('example.com', [
    { spkiSha256: 'sha256/attacker-pin' },
  ]);
  assert.equal(attackerPinCheck.allowed, true);
  assert.equal(attackerPinCheck.enforced, false);
  assert.equal(attackerPinCheck.reason, 'no-static-pins');
});

test('Public-Key-Pins-Report-Only never creates a report', () => {
  let reports = 0;
  const policy = new NetworkSecurityPolicy({
    reportSender: () => {
      reports += 1;
    },
  });

  policy.processResponse('https://reports.example/', {
    'Public-Key-Pins-Report-Only':
      'pin-sha256="sha256/certificate-pin"; report-uri="https://attacker.example/report"',
  });

  assert.equal(reports, 0);
  assert.equal(policy.getDynamicPins('reports.example').length, 0);
  assert.equal(policy.getReportRequests('reports.example').length, 0);
});

test('Strict-Transport-Security is still applied alongside ignored headers', () => {
  const policy = new NetworkSecurityPolicy({ now: () => 10_000 });

  const first = policy.processResponse('https://example.com/', {
    'Strict-Transport-Security':
      'max-age=3600; includeSubDomains; preload',
    'Public-Key-Pins':
      'pin-sha256="sha256/attacker"; max-age=31536000; report-uri="https://attacker.example/hpkp"',
    'Expect-CT': 'max-age=31536000; enforce',
  });

  assert.equal(first.hstsChanged, true);
  assert.ok(first.hsts);
  assert.equal(first.hsts.maxAge, 3600);
  assert.equal(first.hsts.includeSubDomains, true);
  assert.equal(first.hsts.preload, true);

  const second = policy.processResponse('https://example.com/', {
    'Public-Key-Pins':
      'pin-sha256="sha256/another-attacker-pin"; max-age=0',
    'Expect-CT': 'max-age=0; enforce',
  });

  assert.equal(second.hstsChanged, false);
  assert.ok(second.hsts);
  assert.equal(second.hsts.maxAge, 3600);
  assert.equal(policy.getHsts('api.example.com')?.maxAge, 3600);

  const cleared = policy.processResponse('https://example.com/', {
    'Strict-Transport-Security': 'max-age=0',
    'Public-Key-Pins':
      'pin-sha256="sha256/attacker"; max-age=31536000',
  });

  assert.equal(cleared.hstsChanged, true);
  assert.equal(cleared.hsts, null);
});

test('HPKP headers cannot add dynamic pin records to disk', (context) => {
  const directory = createTempDirectory(context);
  const storePath = join(directory, 'pins.json');
  writeFileSync(
    storePath,
    JSON.stringify({
      version: 1,
      pins: [
        {
          host: 'static.example',
          pins: ['sha256/static-pin'],
          source: 'static',
        },
      ],
    }),
  );

  const policy = new NetworkSecurityPolicy({ legacyPinStore: storePath });
  assert.equal(policy.startupPinPurge?.removedDynamic, 0);
  assert.equal(policy.startupPinPurge?.retainedStatic, 1);

  policy.processResponse('https://attacker.example/', {
    'Public-Key-Pins':
      'pin-sha256="sha256/attacker-pin"; max-age=31536000; includeSubDomains; report-uri="https://attacker.example/report"',
    'Public-Key-Pins-Report-Only':
      'pin-sha256="sha256/attacker-pin"; report-uri="https://attacker.example/report"',
  });

  const snapshot = new LegacyPinStore(storePath).readAll();
  assert.equal(snapshot.dynamicCount, 0);
  assert.deepEqual(
    snapshot.records.map((record) => record.host),
    ['static.example'],
  );
  assert.equal(policy.getDynamicPins('attacker.example').length, 0);
});

test('compiled static pins are enforced through the policy', () => {
  const policy = new NetworkSecurityPolicy({
    staticPins: {
      'pinned.example': {
        pins: ['sha256/leaf-pin'],
        includeSubDomains: true,
      },
    },
  });

  const matching = policy.verifyCertificateChain('pinned.example', [
    { spkiSha256: 'sha256/leaf-pin' },
    { spkiSha256: 'sha256/issuer-pin' },
  ]);
  assert.equal(matching.allowed, true);
  assert.equal(matching.enforced, true);
  assert.equal(matching.reason, 'pin-match');

  const mismatching = policy.verifyCertificateChain('pinned.example', [
    { spkiSha256: 'sha256/attacker-pin' },
    { spkiSha256: 'sha256/issuer-pin' },
  ]);
  assert.equal(mismatching.allowed, false);
  assert.equal(mismatching.reason, 'pin-mismatch');

  const inherited = policy.verifyCertificateChain('api.pinned.example', [
    { spkiSha256: 'sha256/leaf-pin' },
  ]);
  assert.equal(inherited.allowed, true);
  assert.equal(inherited.reason, 'pin-match');

  const unpinned = policy.verifyCertificateChain('other.example', [
    { spkiSha256: 'sha256/attacker-pin' },
  ]);
  assert.equal(unpinned.allowed, true);
  assert.equal(unpinned.enforced, false);
  assert.equal(unpinned.reason, 'no-static-pins');
});
