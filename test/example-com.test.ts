import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { auditManifest, type ManifestLike } from '../src/audit.ts';
import { matchesUrl, minimalPatternSet, patternCovers } from '../index.ts';

// Headline acceptance scenario from the task: a `*://*.example.com/*` style
// declaration must cover the bare apex domain `example.com`, and the auditor
// must still surface the over-breadth relative to the declared functionality.

describe('*://*.example.com/* coverage of example.com', () => {
  const wildcard = '*://*.example.com/*';

  it('matches the bare apex domain and every subdomain', () => {
    assert.equal(matchesUrl(wildcard, 'https://example.com/'), true);
    assert.equal(matchesUrl(wildcard, 'http://example.com/api/v2/users'), true);
    assert.equal(matchesUrl(wildcard, 'https://www.example.com/'), true);
    assert.equal(matchesUrl(wildcard, 'https://a.b.example.com/deep'), true);
  });

  it('patternCovers a pattern pinned to the bare domain', () => {
    assert.equal(patternCovers(wildcard, 'https://example.com/api/v2/*'), true);
    assert.equal(patternCovers(wildcard, 'http://example.com/'), true);
  });

  it('does not reach look-alike hosts', () => {
    assert.equal(matchesUrl(wildcard, 'https://notexample.com/'), false);
    assert.equal(matchesUrl(wildcard, 'https://example.com.evil.test/'), false);
  });

  it('the minimal set for a single API endpoint is one exact pattern', () => {
    assert.deepEqual(
      minimalPatternSet({ requiredUrls: ['https://api.example.com/v2/users'] }),
      ['https://api.example.com/v2/users'],
    );
  });

  it('auditManifest flags the wildcard declaration as excess over the real need', () => {
    const manifest: ManifestLike = {
      host_permissions: [wildcard],
      web_accessible_resources: [
        { resources: ['inject.js'], matches: ['https://www.example.com/*'] },
      ],
    };
    const report = auditManifest(manifest, {
      requiredUrls: ['https://api.example.com/v2/users'],
    });

    assert.deepEqual(report.required, ['https://api.example.com/v2/users']);
    assert.equal(report.excess.length, 2);
    const hostPermissionExcess = report.excess.find(
      (item) => item.source === 'host_permissions',
    );
    assert.ok(hostPermissionExcess);
    assert.deepEqual(hostPermissionExcess.reasons, [
      'scheme-too-broad',
      'host-too-broad',
      'path-too-broad',
    ]);
    assert.deepEqual(hostPermissionExcess.suggestedReplacements, [
      'https://api.example.com/v2/users',
    ]);
    assert.deepEqual(report.missing, []);
    // The WAR match lives inside the (broad) host permission, so it is not an
    // additional resource-exposure risk beyond the host permission itself.
    assert.deepEqual(report.webResourceRisks, []);
  });
});
