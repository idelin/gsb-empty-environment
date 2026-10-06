import { TemplateEngine } from '../src/engine';
import {
  TemplateSyntaxError,
  TemplateTypeError,
  UndefinedVariableError,
  UnknownTemplateError,
} from '../src/errors';
import { assert, assertEqual, test } from './harness';

test('variables interpolate from the data scope', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', 'Hello {{ user.name }}, you are {{ user.age }}.');
  const result = engine.render('main', { user: { name: 'Ada', age: 36 } });
  assertEqual(result.text, 'Hello Ada, you are 36.', 'text');
});

test('untaken if-branch never touches its variables', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', '{{#if ok}}yes{{else}}{{ missing.deep }}{{/if}}');
  assertEqual(engine.render('main', { ok: true }).text, 'yes', 'then branch');

  const engine2 = new TemplateEngine();
  engine2.addTemplate('main', '{{#if ok}}{{ missing.deep }}{{else}}no{{/if}}');
  assertEqual(engine2.render('main', { ok: false }).text, 'no', 'else branch');
});

test('taken if-branch still reports undefined variables', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', '{{#if ok}}{{ missing }}{{/if}}');
  let err: UndefinedVariableError | null = null;
  try {
    engine.render('main', { ok: true });
  } catch (e) {
    err = e as UndefinedVariableError;
  }
  assert(err instanceof UndefinedVariableError, 'should throw UndefinedVariableError');
  assertEqual(err!.path, 'missing', 'error path');
});

test('loop variable shadows outer binding and is restored on exit', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', '[{{ item }}]{{#each list as item}}{{ item }}{{/each}}[{{ item }}]');
  const result = engine.render('main', { item: 'outer', list: ['a', 'b'] });
  assertEqual(result.text, '[outer]ab[outer]', 'shadow + restore');
});

test('nested loops with the same variable name restore per level', () => {
  const engine = new TemplateEngine();
  engine.addTemplate(
    'main',
    '{{#each rows as x}}{{#each x.cells as x}}{{ x }};{{/each}}({{ x.name }}){{/each}}',
  );
  const result = engine.render('main', {
    rows: [
      { name: 'r1', cells: ['a', 'b'] },
      { name: 'r2', cells: ['c'] },
    ],
  });
  assertEqual(result.text, 'a;b;(r1)c;(r2)', 'nested shadowing');
});

test('@index is loop-local and restored', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', '{{#each list as v}}{{@index}}:{{v}} {{/each}}');
  assertEqual(engine.render('main', { list: ['x', 'y', 'z'] }).text, '0:x 1:y 2:z ', 'index');
});

test('empty loop produces no output and no scope changes', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', 'A{{#each list as item}}{{ item }}{{/each}}B{{ item }}');
  const result = engine.render('main', { list: [], item: 'still-outer' });
  assertEqual(result.text, 'ABstill-outer', 'empty loop');
});

test('undefined variable error carries template position', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', 'line1\nline2 {{ nope }} tail');
  let err: UndefinedVariableError | null = null;
  try {
    engine.render('main', {});
  } catch (e) {
    err = e as UndefinedVariableError;
  }
  assert(err instanceof UndefinedVariableError, 'is UndefinedVariableError');
  assertEqual(err!.span!.template, 'main', 'template name');
  assertEqual(err!.span!.start.line, 2, 'line');
  assertEqual(err!.span!.start.col, 7, 'col');
  assert(err!.format().includes('main:2:7'), 'format includes position');
});

test('undefined variable inside includes reports the full call stack', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', 'top {{> card }} bottom');
  engine.addTemplate('card', 'card: {{> field }}');
  engine.addTemplate('field', '{{ value }}');
  let err: UndefinedVariableError | null = null;
  try {
    engine.render('main', {});
  } catch (e) {
    err = e as UndefinedVariableError;
  }
  assert(err instanceof UndefinedVariableError, 'is UndefinedVariableError');
  assertEqual(err!.span!.template, 'field', 'innermost template');
  assertEqual(err!.callStack.length, 2, 'two include frames');
  assertEqual(err!.callStack[0].template, 'card', 'first frame target');
  assertEqual(err!.callStack[0].span.template, 'main', 'first frame call site');
  assertEqual(err!.callStack[1].template, 'field', 'second frame target');
  const formatted = err!.format();
  assert(formatted.includes('field:1:1'), 'error site in output');
  assert(formatted.includes("included from card:1:7 (-> field)"), 'inner frame in output');
  assert(formatted.includes("included from main:1:5 (-> card)"), 'outer frame in output');
});

test('output fragments retain provenance spans', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', 'AB{{ x }}CD{{#if y}}E{{/if}}');
  const result = engine.render('main', { x: '1', y: true });
  assertEqual(result.text, 'AB1CDE', 'joined text');
  assertEqual(result.fragments.length, 4, 'fragment count');
  assertEqual(result.fragments[0], {
    text: 'AB',
    span: {
      template: 'main',
      start: { offset: 0, line: 1, col: 1 },
      end: { offset: 2, line: 1, col: 3 },
    },
  }, 'text fragment provenance');
  assertEqual(result.fragments[1].text, '1', 'var fragment text');
  assertEqual(result.fragments[1].span.start, { offset: 2, line: 1, col: 3 }, 'var span start');
  assertEqual(result.fragments[1].span.end, { offset: 9, line: 1, col: 10 }, 'var span end');
  assertEqual(result.fragments[3].text, 'E', 'if body fragment');
  assertEqual(result.fragments[3].span.start.offset, 20, 'if body offset');
});

test('each on a non-array raises a type error with span', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', '{{#each num as n}}{{/each}}');
  let err: TemplateTypeError | null = null;
  try {
    engine.render('main', { num: 42 });
  } catch (e) {
    err = e as TemplateTypeError;
  }
  assert(err instanceof TemplateTypeError, 'is TemplateTypeError');
  assertEqual(err!.span!.template, 'main', 'span template');
});

test('syntax errors carry positions', () => {
  const engine = new TemplateEngine();
  let err: TemplateSyntaxError | null = null;
  try {
    engine.addTemplate('bad', '{{#if a}}x');
  } catch (e) {
    err = e as TemplateSyntaxError;
  }
  assert(err instanceof TemplateSyntaxError, 'unclosed block');
  assertEqual(err!.span!.start.line, 1, 'line of open tag');

  let err2: TemplateSyntaxError | null = null;
  try {
    engine.addTemplate('bad2', '{{/if}}');
  } catch (e) {
    err2 = e as TemplateSyntaxError;
  }
  assert(err2 instanceof TemplateSyntaxError, 'unmatched close');
});

test('unknown include raises UnknownTemplateError with call site', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', '{{> ghost }}');
  let err: UnknownTemplateError | null = null;
  try {
    engine.render('main', {});
  } catch (e) {
    err = e as UnknownTemplateError;
  }
  assert(err instanceof UnknownTemplateError, 'is UnknownTemplateError');
  assertEqual(err!.span!.template, 'main', 'call site template');
});

test('null interpolates as empty, objects as JSON', () => {
  const engine = new TemplateEngine();
  engine.addTemplate('main', '[{{ a }}][{{ b }}]');
  assertEqual(engine.render('main', { a: null, b: { x: 1 } }).text, '[][{"x":1}]', 'null/object');
});
