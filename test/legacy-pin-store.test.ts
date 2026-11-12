import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  LegacyPinStore,
  NetworkSecurityPolicy,
  purgeLegacyDynamicPinsAtStartup,
} from '../index.ts';
import { createTempDirectory } from './helpers.ts';

test('startup purge removes legacy dynamic records and keeps static records', (context) => {
  const directory = createTempDirectory(context);
  const storePath = join(directory, 'pins.json');

  writeFileSync(
    storePath,
    JSON.stringify({
      version: 1,
      pins: [
        {
          host: 'dynamic.example',
          pins: ['sha256/dynamic-pin'],
          source: 'dynamic',
          reportUri: 'https://attacker.example/hpkp',
        },
        {
          host: 'unsourced.example',
          pins: ['sha256/legacy-pin'],
        },
        {
          host: 'static.example',
          pins: ['sha256/static-pin'],
          source: 'static',
        },
      ],
      dynamicPins: {
        'nested-dynamic.example': ['sha256/nested-dynamic-pin'],
      },
      staticPins: {
        'nested-static.example': ['sha256/nested-static-pin'],
      },
    }),
  );

  const result = purgeLegacyDynamicPinsAtStartup(storePath);
  assert.equal(result.existed, true);
  assert.equal(result.removedDynamic, 3);
  assert.equal(result.retainedStatic, 2);
  assert.equal(result.rewritten, true);

  const persisted = JSON.parse(readFileSync(storePath, 'utf8')) as {
    version: number;
    pins: Array<{ host: string; source?: string }>;
  };
  assert.equal(persisted.version, 2);
  assert.deepEqual(
    persisted.pins.map((record) => record.host).sort(),
    ['nested-static.example', 'static.example'],
  );
  assert.ok(persisted.pins.every((record) => record.source === 'static'));

  const snapshot = new LegacyPinStore(storePath).readAll();
  assert.equal(snapshot.dynamicCount, 0);
  assert.equal(snapshot.staticCount, 2);
});

test('policy construction purges stale dynamic pins before certificate checks', (context) => {
  const directory = createTempDirectory(context);
  const storePath = join(directory, 'pins.json');
  writeFileSync(
    storePath,
    JSON.stringify({
      version: 1,
      pins: [
        {
          host: 'pinned.example',
          pins: ['sha256/attacker-root-pin'],
          source: 'dynamic',
          includeSubDomains: true,
        },
      ],
    }),
  );

  const policy = new NetworkSecurityPolicy({
    legacyPinStore: storePath,
    staticPins: {
      'pinned.example': ['sha256/leaf-pin'],
    },
  });

  assert.equal(policy.startupPinPurge?.removedDynamic, 1);
  assert.equal(policy.startupPinPurge?.retainedStatic, 0);
  assert.equal(policy.getDynamicPins('pinned.example').length, 0);

  const diskPins = new LegacyPinStore(storePath).readAll();
  assert.equal(diskPins.records.length, 0);
  assert.equal(diskPins.dynamicCount, 0);

  const result = policy.verifyCertificateChain('pinned.example', [
    { spkiSha256: 'sha256/leaf-pin' },
    { spkiSha256: 'sha256/unrelated-root-pin' },
  ]);
  assert.equal(result.allowed, true);
  assert.equal(result.enforced, true);
  assert.equal(result.reason, 'pin-match');
});

test('a corrupt legacy pin file is replaced with an empty snapshot', (context) => {
  const directory = createTempDirectory(context);
  const storePath = join(directory, 'pins.json');
  writeFileSync(storePath, '{not-json');

  const result = new LegacyPinStore(storePath).purgeDynamicPinsAtStartup();
  assert.equal(result.corrupt, true);
  assert.equal(result.rewritten, true);

  const snapshot = new LegacyPinStore(storePath).readAll();
  assert.equal(snapshot.corrupt, false);
  assert.equal(snapshot.records.length, 0);
});
