import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import {
  analyzeExportSurface,
  createPermissionResolver,
  isSignatureLevel,
  parseManifest,
} from '../src/index.ts';

const here = dirname(fileURLToPath(import.meta.url));
const fixture = readFileSync(join(here, 'fixtures', 'sample-manifest.xml'), 'utf8');

test('unprotected exported and implicitly exported components are reported', () => {
  const manifest = parseManifest(fixture);
  const { findings } = analyzeExportSurface(manifest);

  const unprotected = findings.filter(
    (finding) => finding.type === 'unprotected-exported-component',
  );
  const components = unprotected.map((finding) => finding.component);

  assert.ok(components.includes('com.example.bank.RouterActivity'));
  assert.ok(components.includes('com.example.bank.DeepLinkActivity'));
  assert.ok(components.includes('com.example.bank.BootReceiver'));

  const implicitFinding = unprotected.find(
    (finding) => finding.component === 'com.example.bank.DeepLinkActivity',
  );
  assert.equal(implicitFinding?.severity, 'critical');
  assert.equal(implicitFinding?.evidence.implicit, true);
});

test('signature permission guards block third-party access', () => {
  const manifest = parseManifest(fixture);
  const resolver = createPermissionResolver(manifest);

  const partner = resolver.resolve('com.example.bank.PARTNER');
  assert.equal(partner.level, 'signature');
  assert.equal(partner.customDeclared, true);
  assert.equal(isSignatureLevel(partner.level), true);

  const { findings } = analyzeExportSurface(manifest);
  const partnerFindings = findings.filter(
    (finding) => finding.component === 'com.example.bank.PartnerService',
  );
  assert.equal(partnerFindings.length, 0);
});

test('defaulted custom permissions are treated as normal and flagged', () => {
  const manifest = parseManifest(fixture);
  const resolver = createPermissionResolver(manifest);

  const legacy = resolver.resolve('com.example.bank.LEGACY');
  assert.equal(legacy.level, 'normal');
  assert.equal(legacy.defaultedLevel, true);

  const { findings } = analyzeExportSurface(manifest);
  const weak = findings.find(
    (finding) => finding.component === 'com.example.bank.LegacyService',
  );
  assert.equal(weak?.type, 'weak-permission-guard');
  assert.equal(weak?.severity, 'medium');

  const defaulted = findings.find(
    (finding) => finding.type === 'permission-level-defaulted',
  );
  assert.equal(defaulted?.evidence.permission, 'com.example.bank.LEGACY');
});

test('exported providers require read/write guards and surface URI grant bypass', () => {
  const manifest = parseManifest(fixture);
  const { findings } = analyzeExportSurface(manifest);

  const providerFinding = findings.find(
    (finding) => finding.component === 'com.example.bank.PublicFeedProvider',
  );
  assert.equal(providerFinding?.type, 'unprotected-exported-component');
  assert.equal(providerFinding?.severity, 'high');

  const vault = manifest.components.find(
    (component) => component.name === 'com.example.bank.internal.FileVaultProvider',
  )!;
  assert.equal(vault.exported, false);

  const grant = findings.find(
    (finding) => finding.type === 'uri-permission-grant-bypass',
  );
  assert.equal(grant, undefined);
});

test('application-level permission is used as a fallback guard', () => {
  const xml = `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.fallback">
  <permission android:name="com.example.fallback.SIG" android:protectionLevel="signature" />
  <application android:permission="com.example.fallback.SIG">
    <provider
      android:name=".DataProvider"
      android:authorities="com.example.fallback.data"
      android:exported="true" />
  </application>
</manifest>`;

  const manifest = parseManifest(xml);
  const { findings, surfaces } = analyzeExportSurface(manifest);
  const provider = surfaces.find(
    (surface) => surface.name === 'com.example.fallback.DataProvider',
  );
  assert.equal(provider?.protection.permission, 'com.example.fallback.SIG');
  assert.equal(provider?.protection.level, 'signature');
  assert.equal(
    findings.some(
      (finding) =>
        finding.component === 'com.example.fallback.DataProvider' &&
        finding.type === 'unprotected-exported-component',
    ),
    false,
  );
});
