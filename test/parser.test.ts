import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import {
  analyzeExportSurface,
  parseManifest,
  resolveEffectiveExport,
} from '../src/index.ts';

const here = dirname(fileURLToPath(import.meta.url));
const fixture = readFileSync(join(here, 'fixtures', 'sample-manifest.xml'), 'utf8');

test('parser resolves package, sdk levels and shorthand component names', () => {
  const manifest = parseManifest(fixture);

  assert.equal(manifest.packageName, 'com.example.bank');
  assert.equal(manifest.targetSdk, 28);
  assert.equal(manifest.minSdk, 23);

  const router = manifest.components.find(
    (component) => component.name === 'com.example.bank.RouterActivity',
  );
  assert.ok(router);
  assert.equal(router?.exported, true);
  assert.equal(router?.intentFilters[0]?.actions[0], 'android.intent.action.VIEW');
  assert.deepEqual(router?.intentFilters[0]?.data[0], {
    scheme: 'bank',
    host: 'route',
    port: undefined,
    path: undefined,
    pathPrefix: undefined,
    pathPattern: undefined,
    mimeType: undefined,
  });

  const internal = manifest.components.find(
    (component) => component.name === 'com.example.bank.internal.InternalTransferActivity',
  );
  assert.ok(internal);
  assert.equal(internal?.exported, false);

  const alias = manifest.components.find(
    (component) => component.type === 'activity-alias',
  );
  assert.equal(
    alias?.targetActivity,
    'com.example.bank.internal.InternalTransferActivity',
  );

  const provider = manifest.components.find(
    (component) => component.name === 'com.example.bank.PublicFeedProvider',
  );
  assert.deepEqual(provider?.authorities, ['com.example.bank.feed']);

  assert.deepEqual(manifest.usesPermissions, ['android.permission.INTERNET']);
  assert.equal(
    manifest.permissions.get('com.example.bank.PARTNER')?.protectionLevels[0],
    'signature',
  );
  assert.equal(
    manifest.permissions.get('com.example.bank.LEGACY')?.defaulted,
    true,
  );
});

test('components with intent-filters are implicitly exported below targetSdk 31', () => {
  const manifest = parseManifest(fixture);
  const result = analyzeExportSurface(manifest);

  const byName = new Map(result.surfaces.map((surface) => [surface.name, surface]));

  const deepLink = byName.get('com.example.bank.DeepLinkActivity');
  assert.equal(deepLink?.exported, true);
  assert.equal(deepLink?.implicitlyExported, true);

  const bootReceiver = byName.get('com.example.bank.BootReceiver');
  assert.equal(bootReceiver?.exported, true);
  assert.equal(bootReceiver?.implicitlyExported, true);

  const internal = byName.get('com.example.bank.internal.InternalTransferActivity');
  assert.equal(internal?.exported, false);
  assert.equal(internal?.implicitlyExported, false);

  const alias = byName.get('com.example.bank.AliasEntry');
  assert.equal(alias?.exported, true);
  assert.equal(alias?.implicitlyExported, true);
});

test('implicit export flips to non-exported on targetSdk >= 31 and is flagged', () => {
  const modern = fixture.replace('android:targetSdkVersion="28"', 'android:targetSdkVersion="34"');
  const manifest = parseManifest(modern);
  const result = analyzeExportSurface(manifest);

  const bootReceiver = result.surfaces.find(
    (surface) => surface.name === 'com.example.bank.BootReceiver',
  );
  assert.equal(bootReceiver?.exported, false);

  const missingAttr = result.findings.filter(
    (finding) => finding.type === 'missing-exported-attribute',
  );
  assert.ok(missingAttr.length >= 2);
  assert.ok(
    missingAttr.some(
      (finding) => finding.component === 'com.example.bank.BootReceiver',
    ),
  );
});

test('resolveEffectiveExport covers explicit, implicit and private defaults', () => {
  const manifest = parseManifest(fixture);
  const router = manifest.components.find(
    (component) => component.name === 'com.example.bank.RouterActivity',
  )!;
  const secret = manifest.components.find(
    (component) => component.name === 'com.example.bank.internal.SecretSyncService',
  )!;

  assert.equal(resolveEffectiveExport(router, 28).exported, true);
  assert.equal(resolveEffectiveExport(router, 28).implicit, false);
  assert.equal(resolveEffectiveExport(secret, 28).exported, false);
});
