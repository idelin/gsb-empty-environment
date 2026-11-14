import assert from 'node:assert/strict';
import test from 'node:test';

import {
  TemplateError,
  compileTemplate,
  createRenderer,
  renderTemplate,
} from '../index.ts';

function assertTemplateError(
  action: () => unknown,
  code?: string,
): TemplateError {
  let error: unknown;
  try {
    action();
  } catch (caught) {
    error = caught;
  }
  assert.ok(error instanceof TemplateError, 'expected a TemplateError');
  assert.match(error.message, /^Template error at line \d+, column \d+$/);
  if (code !== undefined) {
    assert.equal(error.code, code);
  }
  return error;
}

test('renders declared variables, filters and limited control structures', () => {
  const template = [
    'Hi {{ user.name | trim | capitalize }}!',
    '{{#if user.admin}}admin{{else if user.member}}member{{else}}guest{{/if}}',
    '{{#each items as item}}{{@index}}={{item.name | upper}}{{#unless @last}},{{/unless}}{{/each}}',
  ].join('');

  const output = renderTemplate(template, {
    user: { name: '  ada  ', admin: false, member: true },
    items: [{ name: 'one' }, { name: 'two' }],
  });

  assert.equal(output, 'Hi Ada!member0=ONE,1=TWO');
});

test('supports with, default, length, truncate, json and compiled templates', () => {
  const compiled = compileTemplate(
    '{{#with account as a}}{{a.name | default("anonymous")}}{{/with}}' +
      ':{{items | length}}' +
      ':{{name | truncate(3)}}' +
      ':{{meta | json}}',
  );

  assert.equal(
    compiled.render({
      account: { name: '' },
      items: ['a', 'b'],
      name: 'abcdef',
      meta: { ok: true },
    }),
    'anonymous:2:abc:{"ok":true}',
  );
});

test('property-chain escapes are rejected without executing constructors', () => {
  const secret = 'template-secret-should-not-leak';
  const previous = process.env.TEMPLATE_SECRET;
  process.env.TEMPLATE_SECRET = secret;

  try {
    const templates = [
      '{{ user.constructor.constructor("return process.env.TEMPLATE_SECRET")() }}',
      '{{ user["constructor"] }}',
      '{{ user["\\u0063onstructor"] }}',
      '{{ user.__proto__ }}',
      '{{ user.prototype }}',
      '{{ this.constructor }}',
      '{{ (user).constructor }}',
      '{{ user[0][1].constructor }}',
    ];

    for (const template of templates) {
      const error = assertTemplateError(
        () => renderTemplate(template, { user: { name: 'Ada' } }),
      );
      assert.ok(!error.message.includes(secret));
      assert.ok(!error.message.includes('constructor'));
      assert.ok(error.line >= 1);
      assert.ok(error.column >= 1);
    }
  } finally {
    if (previous === undefined) delete process.env.TEMPLATE_SECRET;
    else process.env.TEMPLATE_SECRET = previous;
  }
});

test('prototype pollution payloads are ignored and never mutate prototypes', () => {
  const polluted = JSON.parse(
    '{"name":"Ada","__proto__":{"polluted":"yes"}}',
  ) as Record<string, unknown>;
  const context = { user: polluted };

  assert.equal(renderTemplate('{{ user.name }}', context), 'Ada');
  assert.equal(renderTemplate('{{ user | json }}', context), '{"name":"Ada"}');
  assertTemplateError(
    () => renderTemplate('{{ user.__proto__.polluted }}', context),
    'FORBIDDEN_ACCESS',
  );
  assertTemplateError(
    () => renderTemplate('{{ user["__proto__"]["polluted"] }}', context),
    'FORBIDDEN_ACCESS',
  );

  assert.equal(({} as Record<string, unknown>).polluted, undefined);
  assert.equal(Object.getPrototypeOf(context.user), Object.prototype);
});

test('function calls are forbidden and context functions are never invoked', () => {
  let calls = 0;
  const fn = () => {
    calls += 1;
    return 'called';
  };
  const context = { fn, nested: { fn } };

  assert.equal(renderTemplate('{{ fn }}', context), '');
  assert.equal(renderTemplate('{{ nested.fn }}', context), '');
  assertTemplateError(() => renderTemplate('{{ fn() }}', context), 'FORBIDDEN_ACCESS');
  assertTemplateError(
    () => renderTemplate('{{ nested.fn() }}', context),
    'FORBIDDEN_ACCESS',
  );
  assertTemplateError(
    () => renderTemplate('{{ fn | call }}', context),
    'UNKNOWN_FILTER',
  );
  assert.equal(calls, 0);
});

test('accessors are not invoked and context is left unchanged', () => {
  let getterCalls = 0;
  const user = {};
  Object.defineProperty(user, 'secret', {
    enumerable: true,
    get() {
      getterCalls += 1;
      return 'leaked';
    },
  });
  const context = {
    user,
    items: [{ name: 'a' }, { name: 'b' }],
    flag: true,
  };
  const before = {
    userKeys: Object.keys(user),
    items: context.items.map((item) => ({ ...item })),
    flag: context.flag,
  };

  assert.equal(renderTemplate('{{ user.secret }}', context), '');
  assert.equal(
    renderTemplate('{{#each items}}{{name}}{{/each}}', context),
    'ab',
  );
  assert.equal(getterCalls, 0);
  assert.deepEqual(Object.keys(user), before.userKeys);
  assert.deepEqual(context.items, before.items);
  assert.equal(context.flag, before.flag);
});

test('non-enumerable properties are not visible through paths or JSON', () => {
  const user = {};
  Object.defineProperty(user, 'secret', {
    configurable: true,
    enumerable: false,
    value: 'hidden',
  });

  const context = { user };
  assert.equal(renderTemplate('{{ user.secret }}', context), '');
  assert.equal(renderTemplate('{{ user | json }}', context), '{}');
  assert.equal(renderTemplate('{{ this | json }}', context), '{"user":{}}');
});

test('loop budget stops oversized loops quickly', () => {
  const items = new Array(1_000_000).fill('x');
  assertTemplateError(
    () =>
      renderTemplate(
        '{{#each items}}{{this}}{{/each}}',
        { items },
        { maxIterations: 25 },
      ),
    'BUDGET_EXCEEDED',
  );
});

test('output length and node budgets are enforced', () => {
  assertTemplateError(
    () =>
      renderTemplate(
        '{{#each items}}1234567890{{/each}}',
        { items: ['a', 'b', 'c'] },
        { maxOutputLength: 10 },
      ),
    'BUDGET_EXCEEDED',
  );

  assertTemplateError(
    () =>
      renderTemplate(
        '{{a}}{{b}}{{c}}{{d}}{{e}}{{f}}',
        { a: 1, b: 2, c: 3, d: 4, e: 5, f: 6 },
        { maxNodes: 4 },
      ),
    'BUDGET_EXCEEDED',
  );
});

test('missing variables are safe and strict mode reports only a position', () => {
  assert.equal(renderTemplate('Hi {{ missing }}!', {}), 'Hi !');
  assert.equal(
    renderTemplate('{{ missing | default("fallback") }}', {}),
    'fallback',
  );

  const error = assertTemplateError(
    () => renderTemplate('line one\nHi {{ missing }}', {}, { strictVariables: true }),
    'MISSING_VARIABLE',
  );
  assert.equal(error.line, 2);
  assert.ok(error.column >= 1);
});

test('declared variables and path allowlists are enforced', () => {
  assertTemplateError(
    () =>
      renderTemplate(
        '{{ secret }}',
        { secret: 'x' },
        { declaredVariables: ['user'] },
      ),
    'UNDECLARED_VARIABLE',
  );

  assert.equal(
    renderTemplate(
      '{{ user.name }}',
      { user: { name: 'Ada', email: 'ada@example.test' } },
      { allowedPaths: ['user.name'] },
    ),
    'Ada',
  );
  assertTemplateError(
    () =>
      renderTemplate(
        '{{ user.email }}',
        { user: { name: 'Ada', email: 'ada@example.test' } },
        { allowedPaths: ['user.name'] },
      ),
    'FORBIDDEN_ACCESS',
  );
  assertTemplateError(
    () =>
      renderTemplate(
        '{{ user.name.first }}',
        { user: { name: { first: 'Ada' } } },
        { allowedPaths: ['user.name'] },
      ),
    'FORBIDDEN_ACCESS',
  );
});

test('aggregate output cannot bypass variable or path allowlists', () => {
  const context = {
    user: { name: 'Ada', email: 'ada@example.test' },
    secret: 'hidden',
  };

  assert.equal(
    renderTemplate('{{ this | json }}', context, { variables: ['user'] }),
    '{"user":{"name":"Ada","email":"ada@example.test"}}',
  );
  assert.equal(
    renderTemplate('{{ this.secret }}', context, { variables: ['user'] }),
    '',
  );

  assert.equal(
    renderTemplate('{{ user | json }}', context, {
      allowedPaths: ['user.name'],
    }),
    '{"name":"Ada"}',
  );
  assert.equal(
    renderTemplate('{{ this | json }}', context, {
      allowedPaths: ['user.name'],
    }),
    '{"user":{"name":"Ada"}}',
  );
  assertTemplateError(
    () => renderTemplate('{{ this.secret }}', context, {
      allowedPaths: ['user.name'],
    }),
    'FORBIDDEN_ACCESS',
  );
});

test('custom filters receive frozen, prototype-less copies', () => {
  let captured: unknown;
  const inspect = (value: unknown) => {
    captured = value;
    assert.equal(Object.getPrototypeOf(value), null);
    assert.equal(Object.isFrozen(value), true);
    return 'ok';
  };

  assert.equal(
    renderTemplate(
      '{{ user | inspect }}',
      { user: { name: 'Ada', nested: { role: 'admin' } } },
      { filters: { inspect } },
    ),
    'ok',
  );
  const copy = captured as Record<string, unknown>;
  assert.equal(copy.name, 'Ada');
  assert.equal((copy.nested as Record<string, unknown>).role, 'admin');
  assert.equal(Object.getPrototypeOf(copy.nested), null);
});

test('custom filters also receive path-filtered copies', () => {
  let captured: unknown;
  const inspect = (value: unknown) => {
    captured = value;
    return 'ok';
  };

  assert.equal(
    renderTemplate(
      '{{ this | inspect }}',
      {
        user: { name: 'Ada', email: 'ada@example.test' },
        secret: 'hidden',
      },
      {
        allowedPaths: ['user.name'],
        filters: { inspect },
      },
    ),
    'ok',
  );
  assert.deepEqual(
    JSON.parse(JSON.stringify(captured)),
    { user: { name: 'Ada' } },
  );
});

test('filter failures disclose only a template position', () => {
  const error = assertTemplateError(
    () =>
      renderTemplate(
        'before {{ name | explode }} after',
        { name: 'Ada' },
        {
          filters: {
            explode: () => {
              throw new Error('secret filter detail');
            },
          },
        },
      ),
    'UNSAFE_VALUE',
  );
  assert.ok(!error.message.includes('secret filter detail'));
});

test('renderer instances compile and render independently', () => {
  const renderer = createRenderer({
    variables: ['name'],
    filters: { shout: (value: unknown) => `${String(value)}!` },
  });

  assert.equal(renderer.render('{{ name | shout }}', { name: 'Ada' }), 'Ada!');
  const compiled = renderer.compile('{{ name | upper }}');
  assert.equal(compiled.render({ name: 'ada' }), 'ADA');
});

test('prototype-less readonly views are used for arrays and objects', () => {
  const seen: unknown[] = [];
  const filter = (value: unknown) => {
    seen.push(value);
    assert.equal(Object.getPrototypeOf(value), null);
    assert.equal(Object.isFrozen(value), true);
    return 'safe';
  };

  assert.equal(
    renderTemplate(
      '{{ list | inspect }}',
      { list: [{ name: 'a' }] },
      { filters: { inspect: filter } },
    ),
    'safe',
  );
  assert.equal(seen.length, 1);
});
