import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeBuilder } from '../src/tree-builder.ts';
import { serialize } from '../src/serializer.ts';
import { sanitize } from '../src/sanitizer.ts';
import { verifyIdempotency } from '../src/idempotency.ts';
import { SanitizationError } from '../src/types.ts';

test('parse/serialize reaches a fixed point on rich mixed content', () => {
  const input =
    '<section><h2>Title</h2><p>Text with <em>emphasis</em> and ' +
    '<a href="https://example.com">link</a>.</p>' +
    '<ul><li>a</li><li>b</li></ul>' +
    '<math><mrow><mi>x</mi></mrow></math>' +
    '<svg viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/></svg>' +
    '</section>';
  const first = serialize(new TreeBuilder(input).parse());
  const second = serialize(new TreeBuilder(first).parse());
  const third = serialize(new TreeBuilder(second).parse());
  assert.equal(first, second);
  assert.equal(second, third);
});

test('idempotency checker reports namespace drift with location and path', () => {
  const doc = new TreeBuilder(
    '<svg><desc><img src="x"></desc></svg>',
  ).parse();
  const result = verifyIdempotency(doc, serialize(doc));
  assert.equal(result.stable, true);
});

test('sanitizer output survives repeated sanitization unchanged', () => {
  const input =
    '<p>Hello <strong>world</strong></p>' +
    '<svg><circle cx="1" cy="2" r="3"/></svg>';
  const first = sanitize(input);
  const second = sanitize(first.html);
  const third = sanitize(second.html);
  assert.equal(first.html, second.html);
  assert.equal(second.html, third.html);
  assert.equal(third.stable, true);
});

test('adversarial mXSS polyglots never yield executable output', () => {
  const payloads = [
    '<math><mtext><table><mglyph><style><!--</style>' +
      '<img src=x onerror=alert(1)>',
    '<svg><desc><img src=x onerror=alert(1)></desc></svg>',
    '<svg><style><img src=x onerror=alert(1)></style></svg>',
    '<noscript><p title="</noscript><img src=x onerror=alert(1)>">',
    '<math><annotation-xml encoding="text/html">' +
      '<img src=x onerror=alert(1)></annotation-xml></math>',
    '<svg><foreignObject><table><style>/*</style>' +
      '<img src=x onerror=alert(1)></table></foreignObject></svg>',
    '<math><mi><b><mglyph><style><!--</style>' +
      '<img src=x onerror=alert(1)></b></mi></math>',
  ];
  for (const payload of payloads) {
    let output = '';
    try {
      output = sanitize(payload).html;
    } catch (error) {
      assert.ok(error instanceof SanitizationError);
      continue;
    }
    assert.ok(!/on\w+\s*=/i.test(output), `handler survived: ${payload}`);
    assert.ok(!output.toLowerCase().includes('javascript:'), payload);
    assert.ok(!/<script/i.test(output), payload);
    // The accepted output must itself be a stable fixed point.
    assert.equal(sanitize(output).html, output, payload);
  }
});

test('protocol obfuscation matrix is fully neutralized', () => {
  const obfuscated = [
    'javascript:alert(1)',
    'JaVaScRiPt:alert(1)',
    'java&#115;cript:alert(1)',
    '&#106;avascript:alert(1)',
    '  javascript:alert(1)',
    'j\nav\tscript:alert(1)',
    'j\u0000avascript:alert(1)',
    '&amp;#106;avascript:alert(1)',
    'data:text/html;base64,PHNjcmlwdD4=',
    'vbscript:msgbox(1)',
    'livescript:alert(1)',
  ];
  for (const url of obfuscated) {
    const result = sanitize(`<a href="${url}">x</a>`);
    assert.ok(!result.html.includes('href='), url);
    assert.ok(
      result.violations.some((v) => v.kind === 'banned-protocol'),
      url,
    );
  }
});

test('violations carry source locations for payload reporting', () => {
  const result = sanitize('<p>\n  <a href="javascript:alert(1)">x</a></p>');
  const violation = result.violations[0];
  assert.equal(violation.kind, 'banned-protocol');
  assert.ok(violation.location.line >= 1);
  assert.ok(violation.location.offset >= 0);
  assert.ok(violation.path.length > 0);

  // Hard failures still surface as SanitizationError with locations.
  assert.throws(
    () => sanitize('<!-- <img src=x onerror=alert(1)> -->'),
    (error: unknown) => {
      assert.ok(error instanceof SanitizationError);
      assert.ok(error.violations[0].location.offset >= 0);
      return true;
    },
  );
});
