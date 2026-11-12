import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { auditManifest, type ManifestLike } from '../src/audit.ts';

const requirements = {
  requiredUrls: [
    'https://api.example.com/v2/users',
    'https://cdn.example.com/assets/app.js',
  ],
};

describe('auditManifest', () => {
  it('flags <all_urls> as all-urls excess with minimal replacements', () => {
    const manifest: ManifestLike = {
      host_permissions: ['<all_urls>'],
    };
    const report = auditManifest(manifest, requirements);

    assert.equal(report.excess.length, 1);
    assert.equal(report.excess[0].pattern, '<all_urls>');
    assert.deepEqual(report.excess[0].reasons, ['all-urls']);
    assert.deepEqual(report.excess[0].suggestedReplacements, report.required);
    assert.deepEqual(report.missing, []);
  });

  it('flags scheme/host/path over-breadth on a wildcard declaration', () => {
    const manifest: ManifestLike = {
      host_permissions: ['*://*.example.com/*'],
    };
    const report = auditManifest(manifest, requirements);

    assert.equal(report.excess.length, 1);
    const excess = report.excess[0];
    assert.equal(excess.pattern, '*://*.example.com/*');
    assert.ok(excess.reasons.includes('scheme-too-broad'));
    assert.ok(excess.reasons.includes('host-too-broad'));
    assert.ok(excess.reasons.includes('path-too-broad'));
    assert.deepEqual(excess.suggestedReplacements, report.required);
  });

  it('accepts a manifest whose declarations equal the minimal required set', () => {
    const manifest: ManifestLike = {
      host_permissions: [
        'https://api.example.com/v2/users',
        'https://cdn.example.com/assets/app.js',
      ],
    };
    const report = auditManifest(manifest, requirements);
    assert.deepEqual(report.excess, []);
    assert.deepEqual(report.missing, []);
    assert.deepEqual(report.redundant, []);
  });

  it('reports unrequired patterns and missing required coverage', () => {
    const manifest: ManifestLike = {
      host_permissions: ['https://tracker.third-party.test/*'],
    };
    const report = auditManifest(manifest, requirements);

    assert.equal(report.excess[0].reasons[0], 'unrequired');
    assert.deepEqual(report.missing, report.required);
  });

  it('flags redundant patterns covered by another declaration', () => {
    const manifest: ManifestLike = {
      host_permissions: [
        '*://*.example.com/*',
        'https://api.example.com/v2/users',
      ],
    };
    const report = auditManifest(manifest, requirements);

    assert.deepEqual(report.redundant, [
      {
        pattern: 'https://api.example.com/v2/users',
        source: 'host_permissions',
        coveredBy: '*://*.example.com/*',
      },
    ]);
  });

  it('collects invalid patterns instead of throwing', () => {
    const manifest: ManifestLike = {
      host_permissions: ['not-a-pattern'],
    };
    const report = auditManifest(manifest, requirements);
    assert.equal(report.invalid.length, 1);
    assert.equal(report.invalid[0].pattern, 'not-a-pattern');
  });

  it('inspects content_scripts and web_accessible_resources matches', () => {
    const manifest: ManifestLike = {
      host_permissions: ['https://api.example.com/v2/users'],
      content_scripts: [{ matches: ['https://cdn.example.com/assets/*'] }],
      web_accessible_resources: [
        {
          resources: ['emoji.png'],
          matches: ['https://unexpected.example.org/*'],
        },
      ],
    };
    const report = auditManifest(manifest, requirements);

    const sources = report.excess.map((item) => item.source);
    assert.ok(sources.includes('content_scripts'));
    assert.ok(sources.includes('web_accessible_resources'));

    assert.equal(report.webResourceRisks.length, 1);
    assert.deepEqual(report.webResourceRisks[0].exposedBeyondHostPermissions, [
      'https://unexpected.example.org/*',
    ]);
  });

  it('produces no web-resource risk when matches stay inside host permissions', () => {
    const manifest: ManifestLike = {
      host_permissions: ['*://*.example.com/*'],
      web_accessible_resources: [
        { resources: ['emoji.png'], matches: ['https://api.example.com/v2/users'] },
      ],
    };
    const report = auditManifest(manifest, requirements);
    assert.deepEqual(report.webResourceRisks, []);
  });
});
