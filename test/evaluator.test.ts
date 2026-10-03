import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AttributeGrammar,
  CycleError,
  self,
  parent,
  child,
  type ASTNode,
} from '../src/grammar.ts';

const pos = { line: 1, col: 1, offset: 0 };

function node(kind: string, fields: Record<string, unknown> = {}): ASTNode {
  return { kind, pos, ...fields };
}

test('inherited attributes flow top-down, synthesized bottom-up', () => {
  // Root.inh (inherited) -> child.use (inherited-ish read) -> child.synth -> root.result
  const grammar = new AttributeGrammar();
  grammar
    .addAttribute('Root', 'inh', 'inherited')
    .addAttribute('Root', 'result', 'synthesized')
    .addAttribute('Leaf', 'env', 'inherited')
    .addAttribute('Leaf', 'synth', 'synthesized');

  const order: string[] = [];
  grammar
    .addRule('Root', 'inh', [], () => { order.push('Root.inh'); return 41; })
    .addRule('Leaf', 'env', [parent('inh')], (_n, get) => {
      order.push('Leaf.env');
      return get(parent('inh'));
    })
    .addRule('Leaf', 'synth', [self('env')], (_n, get) => {
      order.push('Leaf.synth');
      return (get(self('env')) as number) + 1;
    })
    .addRule('Root', 'result', [child('a', 'synth'), child('b', 'synth')], (_n, get) => {
      order.push('Root.result');
      return (get(child('a', 'synth')) as number) + (get(child('b', 'synth')) as number);
    });

  const root = node('Root', { a: node('Leaf'), b: node('Leaf') });
  const result = grammar.evaluate(root);

  assert.equal(result.get(root, 'result'), 84);
  assert.ok(order.indexOf('Root.inh') < order.indexOf('Leaf.env'),
    'inherited attribute must be computed before dependents below it');
  assert.ok(order.indexOf('Leaf.synth') < order.indexOf('Root.result'),
    'synthesized attribute must be computed before dependents above it');
});

test('detects a direct two-attribute cycle on one node', () => {
  const grammar = new AttributeGrammar();
  grammar
    .addAttribute('N', 'a', 'synthesized')
    .addAttribute('N', 'b', 'synthesized')
    .addRule('N', 'a', [self('b')], (_n, get) => get(self('b')))
    .addRule('N', 'b', [self('a')], (_n, get) => get(self('a')));

  assert.throws(
    () => grammar.evaluate(node('N')),
    (error: unknown) => {
      assert.ok(error instanceof CycleError);
      assert.match(error.message, /circular attribute dependency/);
      assert.match(error.message, /<N>\.a/);
      assert.match(error.message, /<N>\.b/);
      return true;
    },
  );
});

test('detects a cycle spanning parent and child attributes', () => {
  const grammar = new AttributeGrammar();
  grammar
    .addAttribute('Up', 'down', 'inherited')
    .addAttribute('Up', 'up', 'synthesized')
    .addAttribute('Down', 'fromParent', 'inherited')
    .addAttribute('Down', 'toParent', 'synthesized')
    .addRule('Up', 'down', [child('kid', 'toParent')], (_n, get) => get(child('kid', 'toParent')))
    .addRule('Up', 'up', [], () => 0)
    .addRule('Down', 'fromParent', [parent('down')], (_n, get) => get(parent('down')))
    .addRule('Down', 'toParent', [self('fromParent')], (_n, get) => get(self('fromParent')));

  assert.throws(
    () => grammar.evaluate(node('Up', { kid: node('Down') })),
    (error: unknown) => {
      assert.ok(error instanceof CycleError);
      // The cycle path should mention the attributes involved.
      assert.match(error.message, /<Up>\.down/);
      assert.match(error.message, /<Down>\.toParent/);
      return true;
    },
  );
});

test('cycle error message includes node positions', () => {
  const grammar = new AttributeGrammar();
  grammar
    .addAttribute('N', 'a', 'synthesized')
    .addAttribute('N', 'b', 'synthesized')
    .addRule('N', 'a', [self('b')], (_n, get) => get(self('b')))
    .addRule('N', 'b', [self('a')], (_n, get) => get(self('a')));

  const cyclic: ASTNode = { kind: 'N', pos: { line: 3, col: 7, offset: 20 } };
  assert.throws(
    () => grammar.evaluate(cyclic),
    (error: unknown) => {
      assert.ok(error instanceof CycleError);
      assert.match(error.message, /3:7/);
      return true;
    },
  );
});

test('referencing an undeclared attribute is an error', () => {
  const grammar = new AttributeGrammar();
  grammar
    .addAttribute('N', 'a', 'synthesized')
    .addRule('N', 'a', [self('ghost')], (_n, get) => get(self('ghost')));

  assert.throws(() => grammar.evaluate(node('N')), /undeclared attribute <N>\.ghost/);
});

test('a rule without a matching attribute declaration is rejected', () => {
  const grammar = new AttributeGrammar();
  assert.throws(
    () => grammar.addRule('N', 'nope', [], () => 1),
    /undeclared attribute <N>\.nope/,
  );
});

test('attribute without a rule is reported at evaluation time', () => {
  const grammar = new AttributeGrammar();
  grammar.addAttribute('N', 'a', 'synthesized');
  assert.throws(() => grammar.evaluate(node('N')), /no rule defined for attribute <N>\.a/);
});
