// @ts-nocheck
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  DEFAULT_QUOTA,
  SafeTemplate,
  SafeTemplateEngine,
  TemplateParseError,
  TemplateQuotaError,
  TemplateSecurityError,
  builtInFilters,
  parseExpression,
  parseTemplate,
  renderTemplate,
  tokenizeExpression,
} from '../src/index.ts';

const filters = { ...builtInFilters };

function render(source, context = {}, options = {}) {
  return renderTemplate(source, context, { filters, ...options });
}

function assertSecurityError(callback, messageMatch) {
  assert.throws(callback, (error) => {
    assert.ok(error instanceof TemplateSecurityError, `expected TemplateSecurityError, got ${error?.constructor?.name}: ${error?.message}`);
    assert.equal(error.code, 'TEMPLATE_SECURITY_ERROR');
    if (messageMatch) assert.match(error.message, messageMatch);
    return true;
  });
}

function assertQuotaError(callback, quotaName) {
  assert.throws(callback, (error) => {
    assert.ok(error instanceof TemplateQuotaError, `expected TemplateQuotaError, got ${error?.constructor?.name}: ${error?.message}`);
    assert.equal(error.code, 'TEMPLATE_QUOTA_EXCEEDED');
    if (quotaName) assert.equal(error.quota, quotaName);
    return true;
  });
}

test('lexer and parser expose a closed AST without executing code', () => {
  const tokens = tokenizeExpression(`user['na' + "me"] | upper`);
  assert.ok(tokens.some((token) => token.kind === 'string' && token.value === 'na'));

  const ast = parseTemplate(`{{ user.name }}`);
  assert.equal(ast[0].kind, 'Output');
  assert.equal(ast[0].expression.kind, 'Member');
  assert.equal(ast[0].expression.computed, false);

  const computed = parseExpression(`user['na' + 'me']`);
  assert.equal(computed.kind, 'Member');
  assert.equal(computed.computed, true);
  assert.equal(parseTemplate(`{% if ready %}yes{% endif %}`)[0].kind, 'If');
  assert.equal(parseTemplate(`{% for item in items %}{{ item }}{% endfor %}`)[0].kind, 'For');
});

test('renders variables, property access, operators and registered filters', () => {
  const context = {
    user: { firstName: 'Ada', lastName: 'Lovelace', age: 36, active: true },
    score: 91.5,
    tags: ['math', 'engine'],
  };

  assert.equal(render(`{{ user.firstName }} {{ user['lastName'] }}`, context), 'Ada Lovelace');
  assert.equal(render(`{{ user['first' + 'Name'] }}`, context), 'Ada');
  assert.equal(render(`{{ 2 + 3 * 4 }}`, context), '14');
  assert.equal(render(`{{ score >= 90 ? 'A' : 'B' }}`, context), 'A');
  assert.equal(render(`{{ user.active && tags | join('-') | upper }}`, context), 'MATH-ENGINE');
  assert.equal(render(`{{ missing ?? 'fallback' }}`, context), 'fallback');
  assert.equal(render(`{{ tags | length }}:{{ tags[0] }}`, context), '2:math');
  assert.equal(render(`{{ tags.length }}`, context), '2');
});

test('renders if / elseif / else branches', () => {
  const template = `{% if score >= 90 %}A{% elseif score >= 80 %}B{% else %}C{% endif %}`;
  assert.equal(render(template, { score: 95 }), 'A');
  assert.equal(render(template, { score: 83 }), 'B');
  assert.equal(render(template, { score: 70 }), 'C');

  const nested = `{% if user %}{% if user.active %}active{% else %}inactive{% endif %}{% endif %}`;
  assert.equal(render(nested, { user: { active: false } }), 'inactive');
});

test('renders arrays and objects in for loops with loop metadata', () => {
  const template = `{% for user, index in users %}{{ loop.index1 }}.{{ index }}:{{ user.name }}:{{ loop.first }}/{{ loop.last }};{% endfor %}`;
  const users = [{ name: 'Ada' }, { name: 'Grace' }, { name: 'Lin' }];
  assert.equal(
    render(template, { users }),
    '1.0:Ada:true/false;2.1:Grace:false/false;3.2:Lin:false/true;',
  );

  const objectTemplate = `{% for value, key in settings %}{{ key }}={{ value }};{% endfor %}`;
  assert.equal(render(objectTemplate, { settings: { theme: 'dark', page: 2 } }), 'theme=dark;page=2;');

  assert.equal(render(`[{% for n in nums %}{{ n * 2 }}{% if not loop.last %},{% endif %}{% endfor %}]`, { nums: [1, 2, 3] }), '[2,4,6]');
  assert.equal(render(`{% for n in missing %}x{% endfor %}`, {}), '');
});

test('renders a legal complex notification template', () => {
  const template = [
    `{% if user.notifications | length == 0 %}`,
    `No notifications for {{ user.name | upper }}.`,
    `{% else %}`,
    `Hi {{ user.name }}, you have {{ user.notifications | length }} update(s):`,
    `{% for note in user.notifications %}`,
    `- [{{ loop.index1 }}/{{ loop.length }}] {{ note.title | trim }} ({{ note.level | default('info') }}){% if note.unread %} NEW{% endif %}`,
    `{% endfor %}`,
    `Total unread: {{ unreadCount }}.`,
    `{% endif %}`,
  ].join('\n');

  const context = {
    user: {
      name: 'ada',
      notifications: [
        { title: '  Build passed  ', level: 'success', unread: true },
        { title: 'Review requested', unread: false },
        { title: 'Security alert', level: 'critical', unread: true },
      ],
    },
    unreadCount: 2,
  };

  const output = render(template, context);
  assert.match(output, /Hi ada, you have 3 update\(s\):/);
  assert.match(output, /\[1\/3\] Build passed \(success\) NEW/);
  assert.match(output, /\[2\/3\] Review requested \(info\)/);
  assert.match(output, /\[3\/3\] Security alert \(critical\) NEW/);
  assert.match(output, /Total unread: 2\./);

  const emptyOutput = render(template, { user: { name: 'ada', notifications: [] } });
  assert.equal(emptyOutput.trim(), 'No notifications for ADA.');
});

test('blocks dynamically concatenated constructor and prototype keys', () => {
  const context = {
    obj: { safe: 'value' },
    dynamicKey: 'const' + 'ructor',
    protoKey: '__pro' + 'to__',
  };

  const payloads = [
    `{{ obj['con' + 'structor'] }}`,
    `{{ obj["const" + "ructor"] }}`,
    `{{ obj[dynamicKey] }}`,
    `{{ obj[protoKey] }}`,
    `{{ obj['__pro' + 'to__'] }}`,
    `{{ obj.constructor }}`,
    `{{ obj.prototype }}`,
    `{{ obj.__proto__ }}`,
    `{{ obj.__defineGetter__ }}`,
    `{{ obj.__lookupSetter__ }}`,
  ];

  for (const payload of payloads) assertSecurityError(() => render(payload, context));
});

test('blocks prototype chain traversal and inherited members', () => {
  const context = { obj: { own: 1 }, arr: [1, 2] };
  assertSecurityError(() => render(`{{ obj.toString }}`, context), /inherited prototype member/);
  assertSecurityError(() => render(`{{ obj.hasOwnProperty }}`, context));
  assertSecurityError(() => render(`{{ arr.map }}`, context), /inherited prototype member "map"/);
  assertSecurityError(() => render(`{{ arr.join }}`, context));
  assertSecurityError(() => render(`{{ obj.constructor.prototype }}`, context), /blocked property/);

  Object.prototype.templateInjected = 'pwned';
  try {
    assertSecurityError(() => render(`{{ obj.templateInjected }}`, context), /inherited prototype member/);
    assert.equal(render(`{{ obj.own }}`, context), '1');
  } finally {
    delete Object.prototype.templateInjected;
  }
});

test('only exposes own enumerable data properties', () => {
  const obj = { visible: 'yes' };
  Object.defineProperty(obj, 'hidden', { value: 'secret', enumerable: false });
  Object.defineProperty(obj, 'getterValue', {
    enumerable: true,
    get() {
      return 'getter-secret';
    },
  });

  assert.equal(render(`{{ obj.visible }}`, { obj }), 'yes');
  assertSecurityError(() => render(`{{ obj.hidden }}`, { obj }), /not an enumerable own property/);
  assertSecurityError(() => render(`{{ obj.getterValue }}`, { obj }), /Accessor property/);
});

test('blocks array callback escape and arbitrary calls', () => {
  const context = {
    nums: [1, 2, 3],
    callback: () => 'host-function',
    evil: (command) => command,
  };

  const payloads = [
    `{{ nums.map(x => x.constructor) }}`,
    `{{ nums.map(function (x) { return x }) }}`,
    `{{ nums['map'](callback) }}`,
    `{{ nums.map(callback) }}`,
    `{{ callback() }}`,
    `{{ evil('process') }}`,
    `{{ filter.constructor('return process')() }}`,
    `{{ [1].filter.constructor }}`,
  ];

  for (const payload of payloads) {
    assert.throws(() => render(payload, context), (error) => {
      assert.ok(
        error instanceof TemplateSecurityError || error instanceof TemplateParseError,
        `expected security/parse error for ${payload}, got ${error?.name}: ${error?.message}`,
      );
      return true;
    });
  }

  assert.equal(
    render(`{{ nums | join('-') }}`, context, { filters: { join: builtInFilters.join } }),
    '1-2-3',
  );
});

test('whitelist filters are own-property checked and cannot be inherited', () => {
  const inheritedFilters = Object.create({
    evil: () => 'inherited',
  });
  const engine = new SafeTemplateEngine({ filters: Object.assign(inheritedFilters, { upper: builtInFilters.upper }) });

  assert.equal(engine.render(`{{ 'ok' | upper }}`), 'OK');
  assertSecurityError(() => engine.render(`{{ 'ok' | evil }}`));
  assertSecurityError(() => engine.render(`{{ 'ok' | toString }}`));

  const plain = new SafeTemplateEngine({ filters: { upper: builtInFilters.upper } });
  assert.throws(() => plain.render(`{{ 'x' | notRegistered }}`), TemplateSecurityError);
});

test('does not mutate context and rejects assignment syntax', () => {
  const context = { user: { name: 'Ada' }, count: 1 };
  const snapshot = structuredClone(context);

  assert.throws(() => parseTemplate(`{{ user.name = 'Grace' }}`), TemplateParseError);
  assert.throws(() => parseTemplate(`{% set user.name = 'Grace' %}`), TemplateParseError);
  assert.deepEqual(context, snapshot);

  render(`{% for item in items %}{{ item }}{% endfor %}`, { items: ['a', 'b'] });
  assert.deepEqual(context, snapshot);
});

test('enforces AST depth quota for nested expressions and blocks', () => {
  const deepMember = `a${'.b'.repeat(20)}`;
  assertQuotaError(
    () => parseExpression(deepMember, { quotas: { maxAstDepth: 8 } }),
    'maxAstDepth',
  );

  const deepBinary = `${'1 + ('.repeat(30)}1${')'.repeat(30)}`;
  assertQuotaError(
    () => parseExpression(deepBinary, { quotas: { maxAstDepth: 8 } }),
    'maxAstDepth',
  );

  const deepBlocks = `${'{% if ok %}'.repeat(20)}yes${'{% endif %}'.repeat(20)}`;
  assertQuotaError(
    () => parseTemplate(deepBlocks, { quotas: { maxAstDepth: 6 } }),
    'maxAstDepth',
  );

  assert.ok(parseExpression(`a.b.c`, { quotas: { maxAstDepth: 10 } }));
});

test('enforces total loop iteration quota across nested and sibling loops', () => {
  const context = { items: Array.from({ length: 100 }, (_, index) => index) };
  assertQuotaError(
    () => render(`{% for x in items %}{{ x }}{% endfor %}`, context, { quotas: { maxLoopIterations: 25 } }),
    'maxLoopIterations',
  );

  const siblings = `{% for x in items %}.{% endfor %}{% for x in items %}.{% endfor %}`;
  assertQuotaError(
    () => render(siblings, context, { quotas: { maxLoopIterations: 150 } }),
    'maxLoopIterations',
  );

  const cartesian = `{% for a in items %}{% for b in items %}.{% endfor %}{% endfor %}`;
  assertQuotaError(
    () => render(cartesian, { items: Array.from({ length: 40 }, () => 1) }, { quotas: { maxLoopIterations: 500 } }),
    'maxLoopIterations',
  );
});

test('stops loop bombs and unsupported while syntax', () => {
  assert.throws(() => parseTemplate(`{% while true %}{{ 'forever' }}{% endwhile %}`), TemplateParseError);
  assert.throws(() => parseTemplate(`{% loop %}{% endloop %}`), TemplateParseError);

  const huge = Array.from({ length: 1_000_000 }, () => 'x');
  const start = Date.now();
  assertQuotaError(
    () => render(`{% for x in huge %}{{x}}{% endfor %}`, { huge }, {
      quotas: { maxLoopIterations: 100, maxOutputBytes: 10_000, maxEvaluationSteps: 10_000 },
    }),
    'maxLoopIterations',
  );
  assert.ok(Date.now() - start < 1_000);
});

test('enforces output byte quota before oversized rendering', () => {
  assertQuotaError(
    () => render(`abcdefghij`, {}, { quotas: { maxOutputBytes: 5 } }),
    'maxOutputBytes',
  );

  assertQuotaError(
    () => render(`{% for x in items %}é{% endfor %}`, { items: [1, 2, 3] }, { quotas: { maxOutputBytes: 4, maxLoopIterations: 100 } }),
    'maxOutputBytes',
  );

  assert.equal(render(`é`, {}, { quotas: { maxOutputBytes: 2 } }), 'é');
});

test('enforces evaluation step budget', () => {
  assertQuotaError(
    () => render(`{{ a + b * c }}`, { a: 1, b: 2, c: 3 }, { quotas: { maxEvaluationSteps: 4 } }),
    'maxEvaluationSteps',
  );

  const output = render(`{{ a + b }}`, { a: 1, b: 2 }, { quotas: { ...DEFAULT_QUOTA, maxEvaluationSteps: 100 } });
  assert.equal(output, '3');
});

test('SafeTemplate compiles once and renders repeatedly', () => {
  const compiled = new SafeTemplate(`Hello {{ name | upper }}`, { filters: { upper: builtInFilters.upper } });
  assert.equal(compiled.render({ name: 'ada' }), 'Hello ADA');
  assert.equal(compiled.render({ name: 'grace' }), 'Hello GRACE');

  const engine = new SafeTemplateEngine({ filters: { shout: (value) => `${value}!` } });
  assert.equal(engine.render(`{{ name | shout }}`, { name: 'hi' }), 'hi!');
  engine.registerFilter('repeat', (value) => `${value}${value}`);
  assert.equal(engine.render(`{{ name | repeat }}`, { name: 'x' }), 'xx');
});

test('optional HTML escaping is available but does not affect static template text', () => {
  const engine = new SafeTemplateEngine({ escape: 'html' });
  assert.equal(engine.render(`<main>{{ html }}</main>`, { html: `<script>alert(1)</script>` }),
    '<main>&lt;script&gt;alert(1)&lt;/script&gt;</main>');
});

test('template whitespace control and comments are supported', () => {
  const source = `{% if yes -%}\n  Hello {{ name -}} \n{%- endif %}`;
  assert.equal(render(source, { yes: true, name: 'Ada' }), 'Hello Ada');
  assert.equal(render(`A{# secret #}B`), 'AB');
});

test('rejects destructuring and prototype extraction syntax', () => {
  const payloads = [
    `{% for { constructor } in items %}{{ constructor }}{% endfor %}`,
    `{% for [constructor] in items %}{{ constructor }}{% endfor %}`,
    `{{ ({ constructor }) => constructor }}`,
    `{{ values.map(({ constructor: C }) => C) }}`,
    `{{ [...items] }}`,
    `{% for constructor in items %}{{ constructor }}{% endfor %}`,
  ];

  for (const payload of payloads) {
    assert.throws(() => parseTemplate(payload), (error) => {
      assert.ok(
        error instanceof TemplateParseError || error instanceof TemplateSecurityError,
        `expected parse/security error for ${payload}, got ${error?.name}`,
      );
      return true;
    });
  }
});

test('deep unary prefix bombs hit AST quota instead of overflowing', () => {
  const bomb = `${'!'.repeat(5_000)}ready`;
  assertQuotaError(() => parseExpression(bomb, { quotas: { maxAstDepth: 20 } }), 'maxAstDepth');
});

test('strict variables can be enabled explicitly', () => {
  assert.equal(renderTemplate(`{{ missing }}`, {}, { filters }), '');
  assert.throws(
    () => renderTemplate(`{{ missing }}`, {}, { filters, strictVariables: true }),
    /Unknown template variable "missing"/,
  );
});

test('engine compile preserves custom quotas', () => {
  const engine = new SafeTemplateEngine({ quotas: { maxLoopIterations: 1 } });
  assertQuotaError(
    () => engine.compile(`{% for x in xs %}{{x}}{% endfor %}`).render({ xs: [1, 2] }),
    'maxLoopIterations',
  );
});

test('source never imports or invokes eval, Function, or vm', () => {
  const sourcePath = fileURLToPath(new URL('../src/safe-template.ts', import.meta.url));
  const source = readFileSync(sourcePath, 'utf8');
  assert.doesNotMatch(source, /\beval\s*\(/u);
  assert.doesNotMatch(source, /new\s+Function\b/u);
  assert.doesNotMatch(source, /from\s+['"]vm['"]/u);
  assert.doesNotMatch(source, /require\(\s*['"]vm['"]\s*\)/u);
});
