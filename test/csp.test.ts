import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  auditCsp,
  evaluateExternalScript,
  evaluateInlineScript,
  hasBlockingFindings,
  intersectPolicies,
  parsePolicies,
  parseSource,
  resolveDirective,
  resolveScriptSources,
} from '../src/csp.ts';

const codes = (result: ReturnType<typeof auditCsp>): string[] =>
  result.findings.map((finding) => finding.code);

describe('CSP parsing', () => {
  it('parses directives and source expressions', () => {
    const [policy] = parsePolicies(
      "default-src 'self'; script-src 'nonce-abc' https://cdn.example.com data: *",
    );
    const script = policy.directives.get('script-src')!;
    assert.equal(script.sources[0].kind, 'nonce');
    assert.equal(script.sources[0].nonce, 'abc');
    assert.equal(script.sources[1].kind, 'host');
    assert.equal(script.sources[1].host, 'cdn.example.com');
    assert.equal(script.sources[2].kind, 'scheme');
    assert.equal(script.sources[2].scheme, 'data');
    assert.equal(script.sources[3].kind, 'wildcard');
  });

  it('ignores duplicate directives within one header and records them', () => {
    const result = auditCsp(
      "script-src 'self'; script-src 'unsafe-inline'; object-src 'none'; base-uri 'none'",
    );
    const [policy] = result.policies;
    assert.equal(policy.directives.get('script-src')?.value, "'self'");
    assert.deepEqual(policy.duplicates, [
      { name: 'script-src', ignoredValue: "'unsafe-inline'" },
    ]);
    assert.ok(codes(result).includes('DUPLICATE_DIRECTIVE'));
    // The ignored 'unsafe-inline' must not be treated as a live bypass.
    assert.ok(!codes(result).includes('UNSAFE_INLINE'));
  });

  it('classifies hash and nonce sources', () => {
    const nonce = parseSource("'nonce-xyz'");
    assert.equal(nonce.kind, 'nonce');
    const hash = parseSource(
      "'sha256-RNRICH6wK03aj3skm97r8Wz2Gz8vWz2Gz8vWz2Gz8='",
    );
    assert.equal(hash.kind, 'hash');
    assert.equal(hash.hashAlgorithm, 'sha256');
  });
});

describe('Fallback chains', () => {
  it('resolves script-src-elem -> script-src -> default-src', () => {
    const onlyDefault = parsePolicies("default-src 'none'")[0];
    assert.equal(resolveScriptSources(onlyDefault).resolvedFrom, 'default-src');
    assert.equal(
      resolveDirective(onlyDefault, 'script-src')?.name,
      'default-src',
    );

    const scriptAndDefault = parsePolicies(
      "default-src 'none'; script-src 'self'",
    )[0];
    assert.equal(
      resolveScriptSources(scriptAndDefault).resolvedFrom,
      'script-src',
    );

    const elemWins = parsePolicies(
      "default-src 'none'; script-src 'self'; script-src-elem 'none'",
    )[0];
    assert.equal(resolveScriptSources(elemWins).resolvedFrom, 'script-src-elem');
  });

  it('object-src falls back to default-src but base-uri never does', () => {
    const policy = parsePolicies("default-src 'self'")[0];
    assert.equal(resolveDirective(policy, 'object-src')?.name, 'default-src');
    assert.equal(resolveDirective(policy, 'base-uri'), undefined);
  });
});

describe('strict-dynamic semantics', () => {
  it('drops host/scheme/unsafe-inline allowlist when nonce is present', () => {
    const policy = parsePolicies(
      "script-src 'strict-dynamic' 'nonce-a' https://example.com http: 'unsafe-inline'",
    )[0];
    const effective = resolveScriptSources(policy);
    assert.ok(effective.hasStrictDynamic);
    assert.ok(effective.hasNonceOrHash);
    assert.deepEqual(
      effective.sources.map((source) => source.kind).sort(),
      ['nonce', 'strict-dynamic'],
    );
  });

  it('does not flag ignored allowlist entries as live bypasses', () => {
    const result = auditCsp(
      "script-src 'strict-dynamic' 'nonce-a' https://www.google.com 'unsafe-inline'; object-src 'none'; base-uri 'none'",
    );
    assert.ok(!codes(result).includes('UNSAFE_INLINE'));
    assert.ok(!codes(result).includes('JSONP_OR_REDIRECT_SOURCE'));
    assert.ok(!hasBlockingFindings(result));
  });

  it('keeps strict-dynamic findings when no nonce/hash root exists', () => {
    const result = auditCsp(
      "script-src 'strict-dynamic' https://example.com; object-src 'none'; base-uri 'none'",
    );
    assert.ok(codes(result).includes('STRICT_DYNAMIC_WITHOUT_NONCE_OR_HASH'));
  });

  it('rejects inline scripts under strict-dynamic without nonce/hash match', async () => {
    const policies = parsePolicies(
      "script-src 'strict-dynamic' 'nonce-a' https://example.com; object-src 'none'; base-uri 'none'",
    );
    const noMatch = await evaluateInlineScript(policies, 'alert(1)');
    assert.equal(noMatch.allowed, false);
    const withNonce = await evaluateInlineScript(policies, 'alert(1)', 'a');
    assert.equal(withNonce.allowed, true);
  });

  it('matches inline scripts by sha256 hash', async () => {
    const content = 'alert(1)';
    const digest = await crypto.subtle.digest(
      'SHA-256',
      new TextEncoder().encode(content),
    );
    const base64 = Buffer.from(new Uint8Array(digest)).toString('base64');
    const policies = parsePolicies(
      `script-src 'sha256-${base64}'; object-src 'none'; base-uri 'none'`,
    );
    const result = await evaluateInlineScript(policies, content);
    assert.equal(result.allowed, true);
    assert.equal(result.reasons[0].reason, 'hash match');
  });

  it('blocks external allowlist URLs once strict-dynamic is active', () => {
    const policies = parsePolicies(
      "script-src 'strict-dynamic' 'nonce-a' https://cdn.example.com/app.js; object-src 'none'; base-uri 'none'",
    );
    const decision = evaluateExternalScript(
      policies,
      'https://cdn.example.com/app.js',
    );
    assert.equal(decision.allowed, false);
    assert.match(decision.reasons[0].reason, /strict-dynamic/);
  });

  it('ignores host allowlists with strict-dynamic even without a nonce/hash', () => {
    const policies = parsePolicies(
      "script-src 'strict-dynamic' https://cdn.example.com/app.js; object-src 'none'; base-uri 'none'",
    );
    const decision = evaluateExternalScript(
      policies,
      'https://cdn.example.com/app.js',
    );
    assert.equal(decision.allowed, false);
    // The host source must be filtered out of the effective sources.
    const effective = resolveScriptSources(policies[0]);
    assert.ok(!effective.sources.some((s) => s.kind === 'host'));
  });
});

describe('Multi-header intersection', () => {
  it('intersects source tokens across simultaneously enforced policies', () => {
    const policies = parsePolicies([
      "script-src 'self' https://a.example.com https://b.example.com; object-src 'none'; base-uri 'none'",
      "script-src 'self' https://b.example.com https://c.example.com; object-src 'none'; base-uri 'none'",
    ]);
    const merged = intersectPolicies(policies);
    const script = merged.get('script-src')!;
    assert.deepEqual(script.tokens.sort(), ["'self'", 'https://b.example.com']);
  });

  it('drops directives that are missing from any policy', () => {
    const policies = parsePolicies([
      "script-src 'self'; object-src 'none'; base-uri 'none'",
      "script-src 'self'; object-src 'none'",
    ]);
    const merged = intersectPolicies(policies);
    assert.ok(merged.has('object-src'));
    assert.ok(!merged.has('base-uri'));
  });

  it('requires every policy to allow an external script', () => {
    const policies = parsePolicies([
      "script-src https://shared.example.com; object-src 'none'; base-uri 'none'",
      "script-src https://shared.example.com https://other.example.com; object-src 'none'; base-uri 'none'",
    ]);
    const shared = evaluateExternalScript(
      policies,
      'https://shared.example.com/x.js',
    );
    assert.equal(shared.allowed, true);
    const other = evaluateExternalScript(
      policies,
      'https://other.example.com/x.js',
    );
    assert.equal(other.allowed, false);
  });

  it('flags a weak header even when a sibling header is strict', () => {
    const result = auditCsp([
      "script-src 'nonce-a' 'strict-dynamic'; object-src 'none'; base-uri 'none'",
      "script-src 'unsafe-inline'; object-src 'none'; base-uri 'none'",
    ]);
    assert.ok(codes(result).includes('UNSAFE_INLINE'));
    const weakPolicy = result.findings.find(
      (finding) => finding.code === 'UNSAFE_INLINE',
    );
    assert.equal(weakPolicy?.policyIndex, 1);
  });
});

describe('Bypass auditing', () => {
  it('detects unsafe-inline, unsafe-eval, wildcard, data: and http:', () => {
    const result = auditCsp(
      "script-src 'unsafe-inline' 'unsafe-eval' * data: http:; object-src 'none'; base-uri 'none'",
    );
    for (const code of [
      'UNSAFE_INLINE',
      'UNSAFE_EVAL',
      'WILDCARD_SCRIPT',
      'DATA_SCRIPT',
      'BROAD_SCHEME_SCRIPT',
    ]) {
      assert.ok(codes(result).includes(code), `expected ${code}`);
    }
  });

  it('flags known JSONP / open-redirect hosts in script allowlists', () => {
    const result = auditCsp(
      "script-src https://www.google.com https://sub.raw.githubusercontent.com; object-src 'none'; base-uri 'none'",
    );
    const jsonp = result.findings.filter(
      (finding) => finding.code === 'JSONP_OR_REDIRECT_SOURCE',
    );
    assert.equal(jsonp.length, 2);
  });

  it('accepts extra risky hosts via options', () => {
    const result = auditCsp(
      "script-src https://internal-redirect.corp; object-src 'none'; base-uri 'none'",
      { extraRiskyHosts: ['internal-redirect.corp'] },
    );
    assert.ok(codes(result).includes('JSONP_OR_REDIRECT_SOURCE'));
  });

  it('detects <base> hijack exposure from missing/unsafe base-uri', () => {
    const missing = auditCsp("default-src 'self'");
    assert.ok(codes(missing).includes('MISSING_BASE_URI'));
    // object-src falls back to default-src, so it is not missing; but an
    // explicit object-src 'none' is the recommended hardening, surfaced below.
    assert.ok(!codes(missing).includes('MISSING_OBJECT_SRC'));
    assert.ok(!codes(missing).includes('MISSING_DEFAULT_OR_SCRIPT_SRC'));

    const broad = auditCsp(
      "script-src 'self'; object-src 'none'; base-uri https://attacker.test",
    );
    assert.ok(codes(broad).includes('BASE_URI_UNSAFE'));

    const noObjectAtAll = auditCsp("script-src 'self'");
    assert.ok(codes(noObjectAtAll).includes('MISSING_OBJECT_SRC'));

    const safe = auditCsp(
      "script-src 'self'; object-src 'none'; base-uri 'self'",
    );
    assert.ok(!codes(safe).includes('MISSING_BASE_URI'));
    assert.ok(!codes(safe).includes('BASE_URI_UNSAFE'));
  });

  it('requires object-src none and reports unsafe values', () => {
    const result = auditCsp(
      "script-src 'self'; object-src https://plugins.example.com; base-uri 'none'",
    );
    assert.ok(codes(result).includes('OBJECT_SRC_UNSAFE'));
  });

  it('returns no blocking findings for a strict policy', () => {
    const result = auditCsp(
      "script-src 'nonce-a' 'strict-dynamic'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
    );
    assert.equal(hasBlockingFindings(result), false);
  });
});
