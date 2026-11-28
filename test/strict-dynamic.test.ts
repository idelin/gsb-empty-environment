import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { auditCsp, resolveScriptSources, parsePolicies } from '../src/csp.ts';

describe('strict-dynamic edge semantics', () => {
  it('keeps unsafe-inline as a live bypass when strict-dynamic has no nonce/hash', () => {
    const result = auditCsp(
      "script-src 'strict-dynamic' 'unsafe-inline' https://cdn.example.com; object-src 'none'; base-uri 'none'",
    );
    const effective = resolveScriptSources(parsePolicies(result.policies[0].raw)[0]);
    // Host is always ignored under strict-dynamic, but unsafe-inline survives
    // because there is no nonce/hash trusted root.
    assert.ok(!effective.sources.some((s) => s.kind === 'host'));
    assert.ok(effective.sources.some((s) => s.kind === 'unsafe-inline'));
    assert.ok(result.findings.some((f) => f.code === 'UNSAFE_INLINE'));
    assert.ok(
      result.findings.some(
        (f) => f.code === 'STRICT_DYNAMIC_WITHOUT_NONCE_OR_HASH',
      ),
    );
  });

  it('drops unsafe-inline once a nonce trusted root is present', () => {
    const effective = resolveScriptSources(
      parsePolicies(
        "script-src 'strict-dynamic' 'nonce-x' 'unsafe-inline' https://x.test",
      )[0],
    );
    assert.ok(!effective.sources.some((s) => s.kind === 'unsafe-inline'));
    assert.ok(!effective.sources.some((s) => s.kind === 'host'));
    assert.ok(effective.sources.some((s) => s.kind === 'nonce'));
  });
});
