import assert from 'node:assert/strict';
import test from 'node:test';
import { AstNode, at, resetNodeIds } from '../src/ast';
import {
  AttributeGrammar,
  child,
  CycleError,
  Evaluator,
  parent,
  self,
  sibling,
  UndefinedAttributeError,
} from '../src/grammar';

test('synthesized attributes evaluate bottom-up in topological order', () => {
  resetNodeIds();
  const g = new AttributeGrammar();
  g.define('Leaf', {
    attr: 'val',
    kind: 'synthesized',
    dependsOn: () => [],
    compute: (node) => node.field<number>('n'),
  });
  g.define('Sum', {
    attr: 'val',
    kind: 'synthesized',
    dependsOn: () => [child(0, 'val'), child(1, 'val')],
    compute: (_node, ctx) => ctx.get<number>(child(0, 'val')) + ctx.get<number>(child(1, 'val')),
  });

  const root = new AstNode('Sum', at(1, 1))
    .add(new AstNode('Leaf', at(1, 1), { n: 2 }))
    .add(new AstNode('Leaf', at(1, 1), { n: 3 }));

  const result = new Evaluator(g).evaluate(root);
  assert.equal(result.get(root, 'val'), 5);

  // children must be evaluated before the parent
  const leafKeys = root.children.map((c) => `${c.id}.val`);
  const rootKey = `${root.id}.val`;
  for (const leafKey of leafKeys) {
    assert.ok(
      result.evaluationOrder.indexOf(leafKey) < result.evaluationOrder.indexOf(rootKey),
      `expected ${leafKey} to be evaluated before ${rootKey}`,
    );
  }
});

test('inherited attributes propagate top-down', () => {
  resetNodeIds();
  const g = new AttributeGrammar();
  g.define('Node', {
    attr: 'depth',
    kind: 'inherited',
    dependsOn: (node) => (node.parent ? [parent('depth')] : []),
    compute: (node, ctx) => (node.parent ? ctx.get<number>(parent('depth')) + 1 : 0),
  });

  const leaf = new AstNode('Node', at(3, 1));
  const mid = new AstNode('Node', at(2, 1)).add(leaf);
  const root = new AstNode('Node', at(1, 1)).add(mid);

  const result = new Evaluator(g).evaluate(root);
  assert.equal(result.get(root, 'depth'), 0);
  assert.equal(result.get(mid, 'depth'), 1);
  assert.equal(result.get(leaf, 'depth'), 2);

  // parents must be evaluated before children
  const order = result.evaluationOrder;
  assert.ok(order.indexOf(`${root.id}.depth`) < order.indexOf(`${leaf.id}.depth`));
});

test('sibling chaining threads values across a node list', () => {
  resetNodeIds();
  const g = new AttributeGrammar();
  g.define('Root', {
    attr: 'start',
    kind: 'synthesized',
    dependsOn: () => [],
    compute: () => 10,
  });
  g.define('Item', {
    attr: 'in',
    kind: 'inherited',
    dependsOn: (node) =>
      node.childIndex === 0 ? [parent('start')] : [sibling(-1, 'out')],
    compute: (node, ctx) =>
      ctx.get<number>(node.childIndex === 0 ? parent('start') : sibling(-1, 'out')),
  });
  g.define('Item', {
    attr: 'out',
    kind: 'synthesized',
    dependsOn: () => [self('in')],
    compute: (_node, ctx) => ctx.get<number>(self('in')) + 1,
  });

  const items = [new AstNode('Item', at(1, 1)), new AstNode('Item', at(2, 1)), new AstNode('Item', at(3, 1))];
  const root = new AstNode('Root', at(1, 1));
  items.forEach((i) => root.add(i));

  const result = new Evaluator(g).evaluate(root);
  assert.deepEqual(items.map((i) => result.get(i, 'in')), [10, 11, 12]);
  assert.deepEqual(items.map((i) => result.get(i, 'out')), [11, 12, 13]);
});

test('intra-node attribute cycle is detected and reported', () => {
  resetNodeIds();
  const g = new AttributeGrammar();
  g.define('Node', {
    attr: 'a',
    kind: 'synthesized',
    dependsOn: () => [self('b')],
    compute: (_n, ctx) => ctx.get(self('b')),
  });
  g.define('Node', {
    attr: 'b',
    kind: 'synthesized',
    dependsOn: () => [self('a')],
    compute: (_n, ctx) => ctx.get(self('a')),
  });

  assert.throws(
    () => new Evaluator(g).evaluate(new AstNode('Node', at(1, 1))),
    (err: unknown) => {
      if (!(err instanceof CycleError)) return false;
      assert.match(err.message, /circular attribute dependency/);
      assert.match(err.message, /\.a/);
      assert.match(err.message, /\.b/);
      return true;
    },
  );
});

test('cross-node cycle between inherited and synthesized attributes is detected', () => {
  resetNodeIds();
  const g = new AttributeGrammar();
  // parent.total (synthesized) <- child.part (inherited) <- parent.total : cycle
  g.define('Parent', {
    attr: 'total',
    kind: 'synthesized',
    dependsOn: () => [child(0, 'part')],
    compute: (_n, ctx) => ctx.get(child(0, 'part')),
  });
  g.define('Child', {
    attr: 'part',
    kind: 'inherited',
    dependsOn: () => [parent('total')],
    compute: (_n, ctx) => ctx.get(parent('total')),
  });

  const root = new AstNode('Parent', at(1, 1)).add(new AstNode('Child', at(2, 1)));
  assert.throws(
    () => new Evaluator(g).evaluate(root),
    (err: unknown) => {
      if (!(err instanceof CycleError)) return false;
      assert.ok(err.cycle.length >= 2);
      return true;
    },
  );
});

test('referencing an undefined attribute fails fast with a clear error', () => {
  resetNodeIds();
  const g = new AttributeGrammar();
  g.define('Node', {
    attr: 'a',
    kind: 'synthesized',
    dependsOn: () => [self('missing')],
    compute: (_n, ctx) => ctx.get(self('missing')),
  });

  assert.throws(
    () => new Evaluator(g).evaluate(new AstNode('Node', at(1, 1))),
    UndefinedAttributeError,
  );
});
