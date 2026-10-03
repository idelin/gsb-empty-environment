import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parse } from '../src/parser.ts';
import { prettyPrint } from '../src/printer.ts';
import type { Expr, Prop } from '../src/ast.ts';

const num = (value: number): Expr => ({ kind: 'num', value });
const str = (value: string): Expr => ({ kind: 'str', value });
const id = (name: string): Expr => ({ kind: 'ident', name });
const bin = (op: string, left: Expr, right: Expr): Expr => ({ kind: 'binary', op, left, right });
const un = (op: string, argument: Expr): Expr => ({ kind: 'unary', op, argument });
const cond = (testE: Expr, consequent: Expr, alternate: Expr): Expr => ({
  kind: 'cond',
  test: testE,
  consequent,
  alternate,
});
const call = (callee: Expr, ...args: Expr[]): Expr => ({ kind: 'call', callee, args });
const mem = (object: Expr, property: string): Expr => ({ kind: 'member', object, property });
const idx = (object: Expr, index: Expr): Expr => ({ kind: 'index', object, index });
const arr = (...elements: Expr[]): Expr => ({ kind: 'array', elements });
const obj = (...props: Prop[]): Expr => ({ kind: 'object', props });
const prop = (key: string, value: Expr): Prop => ({ key, value });

// Print, re-parse, and require structural identity plus printing idempotence.
function roundTrip(ast: Expr, width = 80): string {
  const printed = prettyPrint(ast, width);
  assert.deepStrictEqual(parse(printed), ast, `round trip failed for:\n${printed}`);
  assert.strictEqual(prettyPrint(parse(printed), width), printed, 'printing is not idempotent');
  return printed;
}

function buildComplexAst(): Expr {
  return cond(
    bin(
      '&&',
      bin('>=', mem(id('response'), 'status'), num(200)),
      bin('<', mem(id('response'), 'status'), num(300)),
    ),
    call(
      mem(id('transformer'), 'normalize'),
      obj(
        prop(
          'user',
          obj(
            prop('id', mem(mem(id('response'), 'payload'), 'userId')),
            prop(
              'displayName',
              bin('||', mem(mem(id('response'), 'payload'), 'name'), str('anonymous')),
            ),
            prop(
              'tags',
              arr(id('alpha'), id('beta'), bin('+', str('tag-'), mem(id('response'), 'kind'))),
            ),
          ),
        ),
        prop(
          'metrics',
          obj(
            prop(
              'score',
              bin('+', bin('*', id('weight'), mem(id('response'), 'rawScore')), id('bias')),
            ),
            prop(
              'history',
              call(
                mem(mem(id('response'), 'history'), 'slice'),
                num(0),
                bin('-', mem(mem(id('response'), 'history'), 'length'), num(1)),
              ),
            ),
          ),
        ),
      ),
      idx(id('fallbacks'), bin('%', id('attemptIndex'), mem(id('fallbacks'), 'length'))),
    ),
    call(
      mem(id('logger'), 'error'),
      str('request failed'),
      cond(un('!', id('retriable')), num(-1), bin('+', id('retryCount'), num(1))),
    ),
  );
}

test('round-trips source snippets through parse -> print -> parse', () => {
  const sources = [
    '1 + 2 * 3',
    '(1 + 2) * 3',
    'a ? b : c ? d : e',
    '(a ? b : c) ? d : e',
    '-x + !y',
    'typeof x === "number" && y instanceof Foo',
    '2 ** 3 ** 4',
    '(2 ** 3) ** 4',
    'foo.bar[0](x, y).baz',
    '{ a: 1, "b c": [1, 2, { d: "e" }] }',
    'x - -5',
    'a || b && c | d ^ e & f === g',
    'f(a ? b : c, [x, y], {})',
    '!!flag',
  ];
  for (const src of sources) {
    const ast = parse(src);
    const printed = prettyPrint(ast, 80);
    assert.deepStrictEqual(parse(printed), ast, `round trip failed for: ${src}`);
  }
});

test('keeps short expressions on one line', () => {
  assert.strictEqual(prettyPrint(bin('+', id('alpha'), bin('*', id('beta'), id('gamma')))), 'alpha + beta * gamma');
  assert.strictEqual(prettyPrint(call(id('foo'), id('a'), id('b'))), 'foo(a, b)');
  assert.strictEqual(prettyPrint(obj(prop('a', num(1)), prop('b', num(2)))), '{ a: 1, b: 2 }');
  assert.strictEqual(prettyPrint(arr(num(1), num(2), num(3))), '[1, 2, 3]');
});

test('adds parentheses only where precedence requires them', () => {
  assert.strictEqual(prettyPrint(bin('*', bin('+', id('a'), id('b')), id('c'))), '(a + b) * c');
  assert.strictEqual(prettyPrint(bin('-', id('a'), bin('-', id('b'), id('c')))), 'a - (b - c)');
  assert.strictEqual(prettyPrint(bin('-', bin('-', id('a'), id('b')), id('c'))), 'a - b - c');
  assert.strictEqual(prettyPrint(un('-', bin('+', id('a'), id('b')))), '-(a + b)');
  assert.strictEqual(prettyPrint(un('!', un('!', id('x')))), '!(!x)');
  assert.strictEqual(prettyPrint(mem(bin('+', id('a'), id('b')), 'c')), '(a + b).c');
  assert.strictEqual(prettyPrint(bin('+', id('a'), cond(id('b'), id('c'), id('d')))), 'a + (b ? c : d)');
});

test('breaks long call argument lists at commas with indentation', () => {
  const ast = call(id('foo'), id('aaaaa'), id('bbbbb'), id('ccccc'));
  assert.strictEqual(prettyPrint(ast, 10), 'foo(\n  aaaaa,\n  bbbbb,\n  ccccc\n)');
});

test('breaks long binary chains at operators with indentation', () => {
  const ast = bin('+', bin('+', id('aaaa'), id('bbbb')), id('cccc'));
  assert.strictEqual(prettyPrint(ast, 8), 'aaaa +\n  bbbb +\n  cccc');
});

test('breaks long object literals at commas with indentation', () => {
  const ast = obj(prop('a', num(1)), prop('b', num(2)));
  assert.strictEqual(prettyPrint(ast, 8), '{\n  a: 1,\n  b: 2\n}');
});

test('breaks long member chains at dots with indentation', () => {
  const ast = mem(mem(mem(id('client'), 'users'), 'active'), 'first');
  assert.strictEqual(prettyPrint(ast, 10), 'client\n  .users\n  .active\n  .first');
});

test('breaks long ternaries at ? and : with indentation', () => {
  const ast = cond(id('condition'), id('consequentValue'), id('alternateValue'));
  assert.strictEqual(
    prettyPrint(ast, 20),
    'condition\n  ? consequentValue\n  : alternateValue',
  );
});

test('respects the 80-column budget on a deeply nested AST', () => {
  const printed = roundTrip(buildComplexAst(), 80);
  const lines = printed.split('\n');
  assert.ok(lines.length > 5, `expected multi-line output, got:\n${printed}`);
  for (const ln of lines) {
    assert.ok(ln.length <= 80, `line exceeds 80 columns (${ln.length}): ${ln}`);
  }
});

test('deeply nested AST round-trips at several widths', () => {
  const ast = buildComplexAst();
  for (const width of [40, 60, 80, 120]) {
    roundTrip(ast, width);
  }
});

test('string escapes survive the round trip', () => {
  const ast = obj(prop('msg', str('say "hi"\n\ttab\\back')));
  const printed = prettyPrint(ast);
  assert.strictEqual(printed, '{ msg: "say \\"hi\\"\\n\\ttab\\\\back" }');
  assert.deepStrictEqual(parse(printed), ast);
});

test('negative number literals round-trip', () => {
  assert.strictEqual(prettyPrint(num(-5)), '-5');
  assert.deepStrictEqual(parse('-5'), num(-5));
  assert.strictEqual(prettyPrint(bin('+', id('x'), num(-5))), 'x + -5');
  roundTrip(bin('+', id('x'), num(-5)));
  roundTrip(call(num(-5), id('x'))); // prints (-5)(x)
});

test('empty collections and zero-argument calls', () => {
  assert.strictEqual(prettyPrint(arr()), '[]');
  assert.strictEqual(prettyPrint(obj()), '{}');
  assert.strictEqual(prettyPrint(call(id('f'))), 'f()');
  roundTrip(obj(prop('a', arr()), prop('b', obj()), prop('c', call(id('f')))));
});
