'use strict';

const assert = require('assert');
const {
  parse,
  astEqual,
  findNode,
  print,
  format,
  replaceNode,
  evaluate,
  ParseError,
} = require('../src');

let passed = 0;
let failed = 0;
const failures = [];

function test(name, fn) {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    failures.push({ name, err });
    console.log(`FAIL ${name}`);
  }
}

const commentTexts = (src) => src.match(/#[^\n]*/g) || [];

// --- 1. Associativity: (a-b)-c vs a-(b-c) --------------------------------

test('(a-b)-c parses left-associative', () => {
  const { ast } = parse('(a-b)-c');
  assert.strictEqual(ast.kind, 'bin');
  assert.strictEqual(ast.op, '-');
  assert.strictEqual(ast.left.kind, 'bin');
  assert.strictEqual(ast.left.op, '-');
  assert.strictEqual(ast.right.kind, 'ident');
});

test('a-b-c equals (a-b)-c structurally', () => {
  assert.ok(astEqual(parse('a-b-c').ast, parse('(a-b)-c').ast));
});

test('a-(b-c) nests on the right and differs from (a-b)-c', () => {
  const { ast } = parse('a-(b-c)');
  assert.strictEqual(ast.kind, 'bin');
  assert.strictEqual(ast.right.kind, 'bin');
  assert.strictEqual(ast.right.op, '-');
  assert.ok(!astEqual(ast, parse('(a-b)-c').ast));
});

test('(a-b)-c and a-(b-c) evaluate differently', () => {
  const env = { vars: { a: 1, b: 2, c: 3 }, funcs: {} };
  assert.strictEqual(evaluate(parse('(a-b)-c').ast, env), -4);
  assert.strictEqual(evaluate(parse('a-(b-c)').ast, env), 2);
});

test('precedence: a+b*c and a*(-b+c)', () => {
  const { ast } = parse('a+b*c');
  assert.strictEqual(ast.op, '+');
  assert.strictEqual(ast.right.op, '*');
  const mixed = parse('a*(-b+c)').ast;
  assert.strictEqual(mixed.op, '*');
  assert.strictEqual(mixed.right.op, '+');
  assert.strictEqual(mixed.right.left.kind, 'neg');
});

// --- 2. Chained unary minus ----------------------------------------------

test('chained unary minus: --x, - -x, ---5, a--b', () => {
  const dd = parse('--x').ast;
  assert.strictEqual(dd.kind, 'neg');
  assert.strictEqual(dd.expr.kind, 'neg');
  assert.strictEqual(dd.expr.expr.kind, 'ident');

  assert.ok(astEqual(parse('- -x').ast, dd));
  assert.ok(astEqual(parse('a--b').ast, parse('a - -b').ast));

  const t = parse('---5').ast;
  assert.strictEqual(t.kind, 'neg');
  assert.strictEqual(t.expr.kind, 'neg');
  assert.strictEqual(t.expr.expr.kind, 'neg');

  const env = { vars: { x: 7, a: 10, b: 4 }, funcs: {} };
  assert.strictEqual(evaluate(dd, env), 7);
  assert.strictEqual(evaluate(parse('a--b').ast, env), 14);
  assert.strictEqual(evaluate(t, env), -5);
});

test('unary minus binds tighter than binary ops', () => {
  const { ast } = parse('-a*b');
  assert.strictEqual(ast.op, '*');
  assert.strictEqual(ast.left.kind, 'neg');
  const env = { vars: { a: 3, b: 2 }, funcs: {} };
  assert.strictEqual(evaluate(ast, env), -6);
});

// --- 3. Empty argument list ----------------------------------------------

test('empty argument list f()', () => {
  const { ast } = parse('f()');
  assert.strictEqual(ast.kind, 'call');
  assert.strictEqual(ast.name, 'f');
  assert.deepStrictEqual(ast.args, []);
  const env = { vars: {}, funcs: { f: () => 42 } };
  assert.strictEqual(evaluate(ast, env), 42);
});

test('f( ) with whitespace and comment inside parens', () => {
  const parsed = parse('f( # nothing\n)');
  assert.strictEqual(parsed.ast.kind, 'call');
  assert.strictEqual(parsed.ast.args.length, 0);
  assert.ok(print(parsed).includes('# nothing'));
});

// --- 4. Multi-line call ---------------------------------------------------

const MULTILINE_CALL = [
  'f(a,   # first arg',
  '  b * 2,',
  '  # standalone note',
  '  g(c))',
].join('\n');

test('multi-line call with comments parses and keeps all comments', () => {
  const parsed = parse(MULTILINE_CALL);
  assert.strictEqual(parsed.ast.kind, 'call');
  assert.strictEqual(parsed.ast.args.length, 3);
  assert.strictEqual(parsed.ast.args[2].kind, 'call');
  const out = print(parsed);
  for (const c of commentTexts(MULTILINE_CALL)) {
    assert.ok(out.includes(c), `missing comment ${c}`);
  }
  assert.ok(astEqual(parse(out).ast, parsed.ast));
});

// --- 5. Tail comments ------------------------------------------------------

test('comments at EOF with no following token stay at the tail', () => {
  const src = 'a + 1 # trailing\n# tail one\n# tail two\n';
  const parsed = parse(src);
  const out = print(parsed);
  assert.ok(out.includes('# trailing'));
  assert.ok(out.includes('# tail one'));
  assert.ok(out.includes('# tail two'));
  assert.ok(out.trimEnd().endsWith('# tail two'));
  assert.deepStrictEqual(commentTexts(out), commentTexts(src));
});

test('comment-only file is preserved', () => {
  const src = '# just a note\n';
  // A comment-only file has no expression; the lexer must still keep the
  // comment on the EOF token. Parsing the expression itself must fail.
  assert.throws(() => parse(src), ParseError);
  const { tokenize } = require('../src');
  const tokens = tokenize(src);
  assert.strictEqual(tokens[tokens.length - 1].type, 'eof');
  assert.strictEqual(tokens[tokens.length - 1].leading[0].text, '# just a note');
});

// --- 6. Round trip: parse(print(ast)) structurally equivalent --------------

const SAMPLES = [
  '(a-b)-c',
  'a-(b-c)',
  'a-b-c',
  '--x',
  '- -x * 3',
  'a--b',
  '---5',
  'f()',
  'f(a, b*2, g(c))',
  MULTILINE_CALL,
  'a + 1 # trailing\n# tail\n',
  '# leading\nx * (y + 2) # same line\n',
  'h(i(j(1)), k)',
  '10/3 - 7/2',
  'a*(b+c)/-d',
];

test('parse(print(ast)) is structurally equivalent for all samples', () => {
  for (const src of SAMPLES) {
    const parsed = parse(src);
    const reparsed = parse(print(parsed));
    assert.ok(astEqual(reparsed.ast, parsed.ast), `round trip changed AST: ${src}`);
  }
});

test('print never loses comments for all samples', () => {
  for (const src of SAMPLES) {
    const out = print(parse(src));
    assert.deepStrictEqual(commentTexts(out), commentTexts(src), `comments changed: ${src}`);
  }
});

// --- 7. Formatting is idempotent -------------------------------------------

test('format(format(src)) === format(src) for all samples', () => {
  for (const src of SAMPLES) {
    const once = format(src);
    assert.strictEqual(format(once), once, `not idempotent: ${src}`);
  }
});

test('formatting does not merge tokens', () => {
  const out = format('a--b');
  assert.ok(astEqual(parse(out).ast, parse('a--b').ast));
  const out2 = format('x  -  -  y');
  assert.ok(astEqual(parse(out2).ast, parse('x--y').ast));
});

// --- 8. Node replacement ----------------------------------------------------

test('replaceNode swaps a node and keeps external comments', () => {
  const src = '# header\na + b # sum line\n# footer\n';
  const parsed = parse(src);
  const target = findNode(parsed.ast, (n) => n.kind === 'ident' && n.name === 'b');
  const out = replaceNode(parsed, target, {
    kind: 'bin', op: '*',
    left: { kind: 'ident', name: 'c' },
    right: { kind: 'num', value: 2 },
  });
  assert.ok(out.includes('# header'));
  assert.ok(out.includes('# sum line'));
  assert.ok(out.includes('# footer'));
  assert.ok(out.includes('c * 2'));
  const reparsed = parse(out);
  assert.strictEqual(reparsed.ast.right.kind, 'bin');
  assert.strictEqual(reparsed.ast.right.op, '*');
});

test('replaceNode on an inner node keeps comments of untouched nodes in order', () => {
  const src = [
    '# file head',
    'f(a, # arg a',
    '  b - c, # difference',
    '  # before g',
    '  g(d)) # call tail',
    '# file tail',
  ].join('\n');
  const parsed = parse(src);
  const target = findNode(parsed.ast, (n) => n.kind === 'ident' && n.name === 'd');
  const out = replaceNode(parsed, target, { kind: 'num', value: 99 });
  assert.deepStrictEqual(commentTexts(out), commentTexts(src));
  assert.ok(astEqual(parse(out).ast, parse(src.replace(/\bg\(d\)/, 'g(99)')).ast));
});

test('replaceNode of the root keeps leading and trailing trivia', () => {
  const src = '# before\na - b # after\n';
  const parsed = parse(src);
  const out = replaceNode(parsed, parsed.ast, {
    kind: 'call', name: 'sub',
    args: [{ kind: 'ident', name: 'a' }, { kind: 'ident', name: 'b' }],
  });
  assert.ok(out.startsWith('# before\n'));
  assert.ok(out.includes('sub(a, b)'));
  assert.ok(out.includes('# after'));
  assert.ok(astEqual(parse(out).ast, parse('sub(a, b)').ast));
});

test('render parenthesizes to preserve AST shape', () => {
  for (const src of ['a-(b-c)', 'a/(b/c)', '(a+b)*c', '-(a+b)', 'a-(b+c)', 'a*(b-c)']) {
    const { ast } = parse(src);
    assert.ok(astEqual(parse(renderOf(ast)).ast, ast), `render changed AST: ${src}`);
  }
  function renderOf(ast) {
    return require('../src').render(ast);
  }
});

// --- 9. Error reporting ------------------------------------------------------

test('parse errors report earliest stuck position and expected set', () => {
  const e1 = catchErr(() => parse('a + '));
  assert.strictEqual(e1.pos, 4);
  assert.ok(e1.expected.includes('identifier'));

  const e2 = catchErr(() => parse('a b'));
  assert.strictEqual(e2.pos, 2);
  assert.ok(e2.expected.includes('end of input'));

  const e3 = catchErr(() => parse('f(a,)'));
  assert.strictEqual(e3.pos, 4);

  const e4 = catchErr(() => parse('(a'));
  assert.strictEqual(e4.pos, 2);
  assert.ok(e4.expected.includes(`')'`));

  const e5 = catchErr(() => parse('a @ b'));
  assert.strictEqual(e5.pos, 2);

  function catchErr(fn) {
    try {
      fn();
    } catch (err) {
      assert.ok(err instanceof ParseError);
      return err;
    }
    throw new Error('expected a ParseError');
  }
});

// --- 10. Interpreter cross-check (no division by zero) -----------------------

test('integer interpreter cross-checks printed and edited output', () => {
  const env = {
    vars: { a: 7, b: 3, c: 2, x: 9, y: 4 },
    funcs: { f: (p, q) => p * 10 + q, g: (p) => -p, h: () => 5 },
  };
  const cases = [
    '(a-b)-c',
    'a-(b-c)',
    'a + b * c - x / y',
    '--x + -y',
    'f(a, b) + g(c) * h()',
    '10/3 - 7/2',
    'a*(b+c)/-x',
  ];
  for (const src of cases) {
    const parsed = parse(src);
    const direct = evaluate(parsed.ast, env);
    const viaPrint = evaluate(parse(print(parsed)).ast, env);
    assert.strictEqual(viaPrint, direct, `semantics changed by print: ${src}`);
  }
  assert.strictEqual(evaluate(parse('10/3').ast, env), 3);
  assert.strictEqual(evaluate(parse('-7/2').ast, env), -3);
  assert.strictEqual(evaluate(parse('7/-2').ast, env), -3);
  assert.throws(() => evaluate(parse('1/0').ast, env), /division by zero/);

  // A semantics-preserving edit must keep the value.
  const parsed = parse('a - b');
  const target = findNode(parsed.ast, (n) => n.kind === 'ident' && n.name === 'b');
  const edited = replaceNode(parsed, target, {
    kind: 'bin', op: '+',
    left: { kind: 'ident', name: 'b' },
    right: { kind: 'num', value: 0 },
  });
  assert.strictEqual(evaluate(parse(edited).ast, env), evaluate(parsed.ast, env));
});

// --- summary -----------------------------------------------------------------

console.log(`\n${passed} passed, ${failed} failed`);
for (const { name, err } of failures) {
  console.log(`\n--- ${name}\n${err.stack}`);
}
process.exit(failed === 0 ? 0 : 1);
