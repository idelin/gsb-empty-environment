import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sanitize } from '../src/sanitizer.ts';
import { SanitizationError } from '../src/types.ts';

function expectRejected(input: string, kind: string) {
  assert.throws(
    () => sanitize(input),
    (error: unknown) => {
      assert.ok(error instanceof SanitizationError);
      assert.ok(
        error.violations.some((violation) => violation.kind === kind),
        `expected violation ${kind}, got ${error.violations
          .map((v) => v.kind)
          .join(',')}`,
      );
      return true;
    },
  );
}

test('keeps benign whitelisted markup stable across re-parse', () => {
  const result = sanitize(
    '<p class="note">Hello <strong>world</strong> ' +
      '<a href="https://example.com" rel="noopener">link</a></p>',
  );
  assert.equal(result.stable, true);
  assert.equal(
    result.html,
    '<p class="note">Hello <strong>world</strong> ' +
      '<a href="https://example.com" rel="noopener">link</a></p>',
  );
});

test('hard-strips script/style/iframe/object/embed/base/meta/noscript', () => {
  const payloads = [
    '<script>alert(1)</script>',
    '<style>body{}</style>',
    '<iframe src="x"></iframe>',
    '<object data="x"></object>',
    '<embed src="x">',
    '<base href="https://evil.example/">',
    '<meta charset="utf-8">',
    '<noscript><img src=x onerror=alert(1)></noscript>',
  ];
  for (const payload of payloads) {
    const result = sanitize(payload);
    assert.equal(result.html, '', payload);
    assert.ok(
      result.violations.some((v) => v.kind === 'banned-element'),
      payload,
    );
  }
});

test('removes every on* event handler regardless of casing', () => {
  const result = sanitize(
    '<img src="x" onerror="alert(1)" OnClick="x()" ONLOAD="y()">',
  );
  assert.equal(result.html, '<img src="x">');
  assert.equal(
    result.violations.filter((v) => v.kind === 'banned-attribute').length,
    3,
  );
});

test('blocks javascript: with numeric entity obfuscation', () => {
  const result = sanitize('<a href="java&#115;cript:alert(1)">x</a>');
  assert.equal(result.html, '<a>x</a>');
  assert.ok(
    result.violations.some((v) => v.kind === 'banned-protocol'),
  );
});

test('blocks javascript: hidden by whitespace and control characters', () => {
  const result = sanitize(
    '<a href="  j\nav&#x09;ascript:alert(1)">x</a>',
  );
  assert.equal(result.html, '<a>x</a>');
  assert.ok(
    result.violations.some((v) => v.kind === 'banned-protocol'),
  );
});

test('blocks double-entity encoded javascript: scheme', () => {
  const result = sanitize(
    '<a href="&amp;#106;avascript:alert(1)">x</a>',
  );
  assert.equal(result.html, '<a>x</a>');
  assert.ok(
    result.violations.some((v) => v.kind === 'banned-protocol'),
  );
});

test('blocks data: and vbscript: schemes', () => {
  assert.equal(
    sanitize('<img src="data:text/html,<script>">').html,
    '<img>',
  );
  assert.equal(
    sanitize('<a href="vbscript:msgbox(1)">x</a>').html,
    '<a>x</a>',
  );
});

test('allows relative, fragment and http/https URLs', () => {
  const result = sanitize(
    '<a href="/path">a</a><a href="#anchor">b</a>' +
      '<a href="https://example.com/x?y=1">c</a>',
  );
  assert.equal(
    result.html,
    '<a href="/path">a</a><a href="#anchor">b</a>' +
      '<a href="https://example.com/x?y=1">c</a>',
  );
});

test('rejects the classic math/mtext/table/mglyph mXSS vector', () => {
  expectRejected(
    '<math><mtext><table><mglyph><style><!--</style>' +
      '<img src=x onerror=alert(1)>',
    'dangerous-nesting',
  );
});

test('rejects table nested inside MathML integration content', () => {
  expectRejected(
    '<math><mtext><table><tr><td>x</td></tr></table></mtext></math>',
    'dangerous-nesting',
  );
});

test('rejects form nested inside foreign content', () => {
  expectRejected(
    '<svg><foreignObject><form><input></form></foreignObject></svg>',
    'dangerous-nesting',
  );
});

test('rejects comments that disguise closing tags', () => {
  expectRejected(
    '<!-- <img src=x onerror=alert(1)> -->',
    'dangerous-comment',
  );
  expectRejected('<!--</style>-->', 'dangerous-comment');
});

test('keeps benign comments when enabled and rejects dangerous ones', () => {
  const result = sanitize('<p>a</p><!-- plain note -->', {
    keepComments: true,
  });
  assert.equal(result.html, '<p>a</p><!-- plain note -->');
});

test('rejects U+FFFD replacement characters by default', () => {
  expectRejected('<p>a\x00b</p>', 'replacement-character');
});

test('re-parsed output is byte-stable and free of handler attributes', () => {
  const payload =
    '<div><img src=x onerror=alert(1)>' +
    '<svg><desc><img src=1 onerror=alert(2)></desc></svg></div>';
  const result = sanitize(payload);
  assert.equal(result.stable, true);
  assert.ok(!result.html.includes('onerror'));
  assert.ok(!result.html.includes('<script'));
});

test('unwrapped non-whitelisted HTML elements keep safe children', () => {
  const result = sanitize('<marquee>safe <b>text</b></marquee>');
  assert.equal(result.html, 'safe <b>text</b>');
});

test('style attribute is blocked unless explicitly enabled and safe', () => {
  const blocked = sanitize('<p style="color:red">x</p>');
  assert.equal(blocked.html, '<p>x</p>');
  const allowed = sanitize('<p style="color: red">x</p>', {
    allowStyleAttribute: true,
  });
  assert.equal(allowed.html, '<p style="color: red">x</p>');
  const unsafe = sanitize(
    '<p style="background:url(javascript:alert(1))">x</p>',
    { allowStyleAttribute: true },
  );
  assert.equal(unsafe.html, '<p>x</p>');
});

test('srcset candidates are individually protocol-checked', () => {
  const blocked = sanitize(
    '<img src="x" srcset="javascript:alert(1) 1x, /safe.png 2x">',
  );
  assert.equal(blocked.html, '<img src="x">');
  const safe = sanitize(
    '<img src="x" srcset="/a.png 1x, https://b.test/b.png 2x">',
  );
  assert.ok(safe.html.includes('srcset='));
});

test('legitimate HTML inside MathML text integration points is retained', () => {
  const result = sanitize(
    '<math><mtext><b>bold</b> normal <i>it</i></mtext></math>',
  );
  assert.equal(
    result.html,
    '<math><mtext><b>bold</b> normal <i>it</i></mtext></math>',
  );
});

test('HTML inside SVG HTML integration points is retained', () => {
  const result = sanitize(
    '<svg><foreignObject><div>html</div></foreignObject></svg>',
  );
  assert.equal(
    result.html,
    '<svg><foreignObject><div>html</div></foreignObject></svg>',
  );
});
