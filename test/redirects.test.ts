import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { analyzeManifest, type RedirectEdge } from '../src/index.ts';

const here = dirname(fileURLToPath(import.meta.url));
const fixture = readFileSync(join(here, 'fixtures', 'sample-manifest.xml'), 'utf8');

const pkg = 'com.example.bank';
const router = `${pkg}.RouterActivity`;
const deepLink = `${pkg}.DeepLinkActivity`;
const partner = `${pkg}.PartnerService`;
const transfer = `${pkg}.internal.InternalTransferActivity`;
const sync = `${pkg}.internal.SecretSyncService`;
const audit = `${pkg}.internal.AuditReceiver`;
const deepAudit = `${pkg}.internal.DeepAuditReceiver`;
const cycleA = `${pkg}.internal.CycleA`;
const cycleB = `${pkg}.internal.CycleB`;
const vault = `${pkg}.internal.FileVaultProvider`;
const alias = `${pkg}.AliasEntry`;

const redirects: RedirectEdge[] = [
  {
    source: router,
    target: transfer,
    kind: 'startActivity',
    intentPassthrough: true,
  },
  {
    source: router,
    target: sync,
    kind: 'bindService',
    intentPassthrough: false,
  },
  {
    source: deepLink,
    target: transfer,
    kind: 'startActivityForResult',
    intentPassthrough: true,
  },
  {
    source: sync,
    target: audit,
    kind: 'sendBroadcast',
    intentPassthrough: true,
  },
  {
    source: audit,
    target: deepAudit,
    kind: 'sendBroadcast',
    intentPassthrough: true,
  },
  // Signature-gated hop: third parties cannot traverse it.
  {
    source: partner,
    target: sync,
    kind: 'bindService',
    requiresPermission: `${pkg}.PARTNER`,
  },
  // Redirect cycle; traversal must terminate.
  { source: cycleA, target: cycleB, kind: 'startActivity' },
  { source: cycleB, target: cycleA, kind: 'startActivity' },
  { source: router, target: cycleA, kind: 'startActivity' },
  {
    source: router,
    target: vault,
    kind: 'content-uri-grant',
    intentPassthrough: false,
  },
];

test('private components reachable only via redirects are identified', () => {
  const report = analyzeManifest(fixture, { redirects });

  const redirectOnlyNames = report.redirectOnlyComponents.map((item) => item.name);
  assert.deepEqual(redirectOnlyNames.sort(), [
    audit,
    cycleA,
    cycleB,
    deepAudit,
    sync,
    transfer,
    vault,
  ]);

  for (const item of report.redirectOnlyComponents) {
    const surface = report.components.find(
      (component) => component.name === item.name,
    )!;
    assert.equal(surface.exported, false, `${item.name} must stay non-exported`);
    assert.equal(surface.reachableViaRedirect, true);
    assert.ok(item.entries.length >= 1);
  }
});

test('full exploitable chains are emitted from exported entries to private targets', () => {
  const report = analyzeManifest(fixture, { redirects });

  const transferChains = report.redirectChains.filter(
    (chain) => chain.target === transfer,
  );
  assert.equal(transferChains.length, 3);

  const viaRouter = transferChains.find((chain) => chain.entry === router)!;
  assert.equal(viaRouter.exploitable, true);
  assert.equal(viaRouter.hopCount, 1);
  assert.equal(viaRouter.targetExported, false);
  assert.equal(viaRouter.intentPassthrough, true);
  assert.deepEqual(
    viaRouter.hops.map((hop) => [hop.from, hop.to, hop.kind]),
    [[router, transfer, 'startActivity']],
  );

  const viaAlias = transferChains.find((chain) => chain.entry === alias)!;
  assert.equal(viaAlias.exploitable, true);
  assert.equal(viaAlias.hops[0]?.kind, 'activity-alias');

  const multiHop = report.redirectChains.find(
    (chain) => chain.target === deepAudit && chain.entry === router,
  )!;
  assert.equal(multiHop.hopCount, 3);
  assert.deepEqual(
    multiHop.hops.map((hop) => hop.to),
    [sync, audit, deepAudit],
  );
  assert.equal(multiHop.exploitable, true);
});

test('signature-protected entries and gated hops are marked non-exploitable', () => {
  const report = analyzeManifest(fixture, { redirects });

  const fromPartner = report.redirectChains.filter(
    (chain) => chain.entry === partner,
  );
  assert.ok(fromPartner.length >= 1);
  for (const chain of fromPartner) {
    assert.equal(chain.entryAccessible, false);
    assert.equal(chain.exploitable, false);
    assert.equal(chain.severity, 'info');
  }

  const secretSync = report.redirectOnlyComponents.find(
    (item) => item.name === sync,
  )!;
  const partnerEntry = secretSync.entries.find((item) => item.entry === partner);
  assert.equal(partnerEntry?.exploitable, false);
  const routerEntry = secretSync.entries.find((item) => item.entry === router);
  assert.equal(routerEntry?.exploitable, true);
  assert.equal(secretSync.exploitable, true);
});

test('redirect cycles terminate and still expose reachable private components', () => {
  const report = analyzeManifest(fixture, { redirects, maxHops: 8 });

  const cycleTargets = report.redirectOnlyComponents.filter((item) =>
    [cycleA, cycleB].includes(item.name),
  );
  assert.equal(cycleTargets.length, 2);

  const chainToA = report.redirectChains.find(
    (chain) => chain.target === cycleA && chain.entry === router,
  )!;
  assert.equal(chainToA.hopCount, 1);

  const chainToB = report.redirectChains.find(
    (chain) => chain.target === cycleB && chain.entry === router,
  )!;
  assert.ok(chainToB.hopCount <= 3);
  assert.ok(
    report.redirectChains.every((chain) => new Set(
      [chain.entry, ...chain.hops.map((hop) => hop.to)],
    ).size === chain.hopCount + 1),
    'chains must not repeat a component',
  );
});

test('URI grant redirects into private providers are flagged as bypasses', () => {
  const report = analyzeManifest(fixture, { redirects });

  const vaultChain = report.redirectChains.find(
    (chain) => chain.target === vault && chain.entry === router,
  )!;
  assert.equal(vaultChain.exploitable, true);
  assert.equal(vaultChain.uriGrantBypass, true);
  assert.equal(vaultChain.severity, 'high');

  const finding = report.findings.find(
    (item) =>
      item.type === 'redirect-to-private-component' &&
      item.component === vault,
  )!;
  assert.equal(finding.evidence.uriGrantBypass, true);
});

test('report summary counts match the analyzed surface', () => {
  const report = analyzeManifest(fixture, { redirects });

  assert.equal(report.summary.componentCount, 12);
  assert.equal(report.summary.redirectOnlyCount, 7);
  assert.equal(
    report.summary.exploitableChainCount,
    report.redirectChains.filter((chain) => chain.exploitable).length,
  );
  assert.ok(report.summary.implicitlyExportedCount >= 3);
  assert.ok(report.summary.unprotectedExportedCount >= 3);
});
