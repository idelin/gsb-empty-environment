import { test } from 'node:test';
import assert from 'node:assert/strict';
import { lex, ParseError } from '../src/lexer.js';
import { parse, children } from '../src/parser.js';
import { print, format } from '../src/printer.js';
import { evaluate } from '../src/interp.js';
import { find, replaceNode, parseExpr } from '../src/edit.js';

// ---- helpers -------------------------------------------------------------

// Structural view of an AST: drops ranges and comment attachments.
function strip(node) {
  switch (node.kind) {
    case 'num': return { kind: 'num', value: node.value };
    case 'ident': return { kind: 'ident', name: node.name };
    case 'unary': return { kind: 'unary', op: node.op, arg: strip(node.arg) };
    case 'binary':
      return { kind: 'binary', op: node.op, left: strip(node.left), right: strip(node.right) };
    case 'call': return { kind: 'call', name: node.name, args: node.args.map(strip) };
    case 'paren': return { kind: 'paren', expr: strip(node.expr) };
    default: throw new Error(`bad kind ${node.kind}`);
  }
}

// Like strip, but erases parentheses: compares computed structure.
function shapeOf(src) {
  const walk = (n) => {
    switch (n.kind) {
      case 'paren': return walk(n.expr);
      case 'num': return { kind: 'num', value: n.value };
      case 'ident': return { kind: 'ident', name: n.name };
      case 'unary': return { kind: 'unary', op: n.op, arg: walk(n.arg) };
      case 'binary': return { kind: 'binary', op: n.op, left: walk(n.left), right: walk(n.right) };
      case 'call': return { kind: 'call', name: n.name, args: n.args.map(walk) };
      default: throw new Error(`bad kind ${n.kind}`);
    }
  };
  return walk(parse(src).root);
}

// Catch a ParseError and return it.
function throwsParse(fn) {
  try {
    fn();
  } catch (e) {
    assert.ok(e instanceof ParseError, `expected ParseError, got ${e}`);
    return e;
  }
  assert.fail('expected a ParseError');
}

// All comments of a document in source order.
function commentSeq(doc) {
  const seq = [];
  const walk = (node) => {
    for (const c of node.leading) seq.push(c.text);
    for (const child of children(node)) walk(child);
    if (node.trailing) seq.push(node.trailing.text);
  };
  walk(doc.root);
  for (const c of doc.tail) seq.push(c.text);
  return seq;
}

const SAMPLES = [
  '(a-b)-c',
  'a-(b-c)',
  'a-b-c',
  '--x + - -y',
  'f()',
  'f(\n  a, // first arg\n  // standalone note\n  b\n)',
  'a + b // trailing',
  'a + b\n// tail comment\n',
  'x * (y + 1) // mult\n- z // sub\n',
  'g(a, h(b, c), 12) * -3 // combo',
  '// header\na   +\n// middle\nb // after b\n+ c\n// tail\n',
];

// ---- grammar: associativity & precedence ---------------------------------

test('(a-b)-c and a-(b-c) parse differently; a-b-c is left-associative', () => {
  const grouped = shapeOf('(a-b)-c');
  const right = shapeOf('a-(b-c)');
  const plain = shapeOf('a-b-c');
  assert.deepEqual(plain, grouped);
  assert.notDeepEqual(plain, right);
  // left-assoc: root op is '-', left child is itself a '-'
  assert.equal(plain.left.kind, 'binary');
  assert.equal(plain.left.left.kind, 'ident');
  assert.equal(right.right.kind, 'binary');
});

test('associativity shows up in evaluation', () => {
  const env = { a: 10, b: 4, c: 1 };
  assert.equal(evaluate(parse('(a-b)-c').root, env), 5);
  assert.equal(evaluate(parse('a-(b-c)').root, env), 7);
  assert.equal(evaluate(parse('a-b-c').root, env), 5);
});

test('multiplication/division bind tighter than plus/minus', () => {
  assert.deepEqual(shapeOf('a+b*c'), shapeOf('a+(b*c)'));
  assert.notDeepEqual(shapeOf('a+b*c'), shapeOf('(a+b)*c'));
  assert.equal(evaluate(parse('2+3*4').root), 14);
  assert.equal(evaluate(parse('(2+3)*4').root), 20);
  assert.equal(evaluate(parse('100/10/5').root), 2); // left assoc
});

// ---- unary minus chains ---------------------------------------------------

test('consecutive unary minus', () => {
  const env = { x: 5, y: 7 };
  assert.equal(evaluate(parse('--x').root, env), 5);
  assert.equal(evaluate(parse('---x').root, env), -5);
  assert.equal(evaluate(parse('- - -x').root, env), -5);
  assert.equal(evaluate(parse('2 * --3').root), 6);
  assert.equal(evaluate(parse('10 - -4').root), 14);
  // printing never merges the two minus tokens
  assert.equal(format('a - -b'), 'a - -b\n');
  assert.equal(format('- -x'), '--x\n');
  assert.deepEqual(strip(parse(format('- -x')).root), strip(parse('- -x').root));
});

// ---- calls ----------------------------------------------------------------

test('empty argument list', () => {
  const doc = parse('f()');
  assert.equal(doc.root.kind, 'call');
  assert.equal(doc.root.args.length, 0);
  assert.equal(format('f(  )'), 'f()\n');
  assert.equal(evaluate(parse('f()').root, { f: () => 42 }), 42);
});

test('multi-line call keeps comments attached to the right nodes', () => {
  const src = 'f(\n  a, // first arg\n  // standalone note\n  b\n)';
  const doc = parse(src);
  assert.deepEqual(commentSeq(doc), ['// first arg', '// standalone note']);
  const a = doc.root.args[0];
  const b = doc.root.args[1];
  assert.equal(a.trailing.text, '// first arg');
  assert.deepEqual(b.leading.map((c) => c.text), ['// standalone note']);
  const out = format(src);
  assert.deepEqual(commentSeq(parse(out)), ['// first arg', '// standalone note']);
});

test('call evaluation with multiple args', () => {
  const env = { g: (x, y) => x * 10 + y, h: (v) => v + 1 };
  assert.equal(evaluate(parse('g(h(1), 2)').root, env), 22);
});

// ---- comment attachment rules ---------------------------------------------

test('trailing comment attaches to last token of the line', () => {
  const doc = parse('a + b // done\n');
  assert.equal(doc.root.trailing.text, '// done');
  assert.equal(doc.tail.length, 0);
});

test('standalone comment attaches to the next token', () => {
  const doc = parse('// hello\na');
  assert.deepEqual(doc.root.leading.map((c) => c.text), ['// hello']);
});

test('comments with no following token stay at the tail', () => {
  const doc = parse('a + b\n// tail one\n// tail two\n');
  assert.equal(doc.tail.length, 2);
  const out = print(doc);
  assert.ok(out.includes('// tail one\n// tail two'));
  assert.deepEqual(commentSeq(parse(out)), ['// tail one', '// tail two']);
});

// ---- round-trip & idempotency ---------------------------------------------

test('parse(print(ast)) is structurally equivalent for all samples', () => {
  for (const src of SAMPLES) {
    const once = parse(src);
    const twice = parse(print(once));
    assert.deepEqual(strip(twice.root), strip(once.root), `structure changed for: ${src}`);
    assert.deepEqual(commentSeq(twice), commentSeq(once), `comments changed for: ${src}`);
  }
});

test('formatting is idempotent', () => {
  for (const src of SAMPLES) {
    assert.equal(format(format(src)), format(src), `not idempotent for: ${src}`);
  }
});

test('formatting preserves token sequence (no merged or lost tokens)', () => {
  for (const src of SAMPLES) {
    const before = lex(src).tokens.map((t) => `${t.type}:${t.value}`);
    const after = lex(format(src)).tokens.map((t) => `${t.type}:${t.value}`);
    assert.deepEqual(after, before, `tokens changed for: ${src}`);
  }
});

// ---- node replacement ------------------------------------------------------

test('replacing a node keeps its external comments', () => {
  const doc = parse('// before\nx + (a - b) // keep\n// tail\n');
  const target = find(doc.root, (n) => n.kind === 'paren');
  replaceNode(doc.root, target, parseExpr('c * d'));
  const out = print(doc);
  assert.ok(out.includes('c * d'));
  assert.ok(!out.includes('a - b'));
  // external comments of the replaced node and of the file survive, in order
  assert.deepEqual(commentSeq(parse(out)), ['// before', '// keep', '// tail']);
  assert.deepEqual(shapeOf(out), shapeOf('x + c * d'));
});

test('replacing the root keeps root-level comments', () => {
  const doc = parse('// head\na + b // tail-of-line\n');
  doc.root = replaceNode(doc.root, doc.root, parseExpr('f(1)'));
  const out = print(doc);
  assert.deepEqual(commentSeq(parse(out)), ['// head', '// tail-of-line']);
  assert.ok(out.includes('f(1)'));
});

test('unchanged nodes keep their comment order after replacement', () => {
  const src = 'f( // note\n  a, // on a\n  b // on b\n)\n';
  const doc = parse(src);
  const before = commentSeq(doc);
  const target = find(doc.root, (n) => n.kind === 'ident' && n.name === 'a');
  replaceNode(doc.root, target, parseExpr('x + 1'));
  const after = commentSeq(parse(print(doc)));
  assert.deepEqual(after, before);
  assert.deepEqual(before, ['// note', '// on a', '// on b']);
});

// ---- error reporting -------------------------------------------------------

test('error at end of input reports position and expected set', () => {
  const src = 'a +';
  const err = throwsParse(() => parse(src));
  assert.equal(err.pos, src.length);
  for (const e of ["'-'", 'identifier', 'integer', "'('"]) {
    assert.ok(err.expected.includes(e), `missing ${e} in ${err.expected}`);
  }
});

test('unclosed paren reports the expected closing token', () => {
  const err = throwsParse(() => parse('(a'));
  assert.equal(err.pos, 2);
  assert.deepEqual(err.expected, ["')'"]);
});

test('error inside argument list points at the offending token', () => {
  const src = 'f(a,)';
  const err = throwsParse(() => parse(src));
  assert.equal(err.pos, 4); // at ')'
  assert.ok(err.expected.includes('identifier'));
});

test('leftover tokens after a complete expression are an error', () => {
  const err = throwsParse(() => parse('a b'));
  assert.equal(err.pos, 2);
  assert.ok(err.expected.includes('end of input'));
});

test('earliest failing position is reported', () => {
  const src = 'a + ) b';
  const err = throwsParse(() => parse(src));
  assert.equal(err.pos, 4); // the ')'
});

test('illegal character is a lex error with position', () => {
  const err = throwsParse(() => parse('2 @ 3'));
  assert.equal(err.pos, 2);
  assert.equal(err.line, 1);
  assert.equal(err.col, 3);
});

// ---- independent interpreter checks (no division-by-zero samples) ---------

test('integer semantics on division-free-of-zero samples', () => {
  const cases = [
    ['10/3', {}, 3],
    ['-7/2', {}, -3],
    ['7/-2', {}, -3],
    ['(2+3)*4-6/2', {}, 17],
    ['100/10/5', {}, 2],
    ['2*3-4/2', {}, 4],
    ['--5', {}, 5],
    ['a*(b+2)-c/3', { a: 3, b: 4, c: 9 }, 15],
  ];
  for (const [src, env, want] of cases) {
    assert.equal(evaluate(parse(src).root, env), want, src);
    // formatting must not change semantics
    assert.equal(evaluate(parse(format(src)).root, env), want, `formatted: ${src}`);
  }
});

test('division by zero is reported by the interpreter', () => {
  assert.throws(() => evaluate(parse('1/0').root), /division by zero/);
  assert.throws(() => evaluate(parse('1/(2-2)').root), /division by zero/);
});
