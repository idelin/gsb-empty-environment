import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DebugVariableModel,
  InvalidHandleError,
  StaleHandleError,
  compareKeys,
  type VariableNode,
} from '../src/model.ts';

/**
 * Virtual object graph with sharing, a cycle, and a getter:
 *
 *   root ──left──▶ { shared } ──┐
 *     │────right─▶ { shared } ──┤ (same object both times)
 *     │────self───▶ root  (cycle)
 *     │────list───▶ [shared, root]
 *     └────secret: getter (counted, may throw)
 */
function buildGraph() {
  const shared = { label: 'shared' };
  const root: Record<string, unknown> = { name: 'root' };
  root.left = { shared };
  root.right = { shared };
  root.self = root;
  root.list = [shared, root];
  let getterCalls = 0;
  Object.defineProperty(root, 'secret', {
    enumerable: true,
    get() {
      getterCalls += 1;
      return 42;
    },
  });
  return {
    root,
    shared,
    getterCalls: () => getterCalls,
  };
}

function childByName(nodes: VariableNode[], name: string): VariableNode {
  const node = nodes.find((n) => n.name === name);
  assert.ok(node, `expected child ${name}`);
  return node;
}

test('shared objects keep one identity and are not copied', () => {
  const model = new DebugVariableModel();
  const { root, shared } = buildGraph();
  const rootNode = model.createRoot('root', root);

  const children = model.expand(rootNode.reference).nodes;
  const left = childByName(children, 'left');
  const right = childByName(children, 'right');

  const leftShared = childByName(model.expand(left.reference).nodes, 'shared');
  const rightShared = childByName(model.expand(right.reference).nodes, 'shared');

  assert.equal(leftShared.reference, rightShared.reference);
  assert.equal(leftShared.objectId, rightShared.objectId);

  // Re-expanding the parent yields the same handle again (stable id).
  const again = childByName(model.expand(left.reference).nodes, 'shared');
  assert.equal(again.reference, leftShared.reference);

  // The handle really points at the one shared object.
  const sharedChildren = model.expand(leftShared.reference).nodes;
  assert.deepEqual(
    sharedChildren.map((n) => [n.name, n.value]),
    [['label', '"shared"']],
  );
  assert.equal(shared.label, 'shared');
});

test('cycles are detectable via stable objectIds and flagged via ancestors', () => {
  const model = new DebugVariableModel();
  const { root } = buildGraph();
  const rootNode = model.createRoot('root', root);
  assert.ok(rootNode.objectId !== undefined);

  const children = model.expand(rootNode.reference, {
    ancestors: [rootNode.objectId],
  }).nodes;

  const self = childByName(children, 'self');
  assert.equal(self.objectId, rootNode.objectId);
  assert.equal(self.cycle, true);

  // Without ancestor context the node is still expandable, but unflagged.
  const plain = childByName(model.expand(rootNode.reference).nodes, 'self');
  assert.equal(plain.cycle, undefined);
  assert.equal(plain.reference, rootNode.reference);

  // Cycle through an array element: list[1] === root.
  const list = childByName(children, 'list');
  const elements = model.expand(list.reference, {
    ancestors: [rootNode.objectId!],
  }).nodes;
  assert.equal(elements[1]!.objectId, rootNode.objectId);
  assert.equal(elements[1]!.cycle, true);
});

test('a new pause generation invalidates old handles', () => {
  const model = new DebugVariableModel();
  const { root } = buildGraph();
  const rootNode = model.createRoot('root', root);
  const generationBefore = model.currentGeneration;

  model.expand(rootNode.reference); // works while the pause is current

  const generationAfter = model.beginPause();
  assert.notEqual(generationAfter, generationBefore);

  assert.throws(() => model.expand(rootNode.reference), StaleHandleError);
  try {
    model.expand(rootNode.reference);
  } catch (error) {
    assert.ok(error instanceof StaleHandleError);
    assert.equal(error.handleGeneration, generationBefore);
    assert.equal(error.currentGeneration, generationAfter);
  }

  // Fresh handles work in the new generation; ids are re-minted per pause.
  const fresh = model.createRoot('root', root);
  assert.ok(model.expand(fresh.reference).total > 0);
});

test('unknown handles raise InvalidHandleError', () => {
  const model = new DebugVariableModel();
  assert.throws(() => model.expand(9999), InvalidHandleError);
});

test('children are sorted by key and paginated', () => {
  const model = new DebugVariableModel();
  const bag = { b: 1, a: 2, c: 3, 10: 'x', 2: 'y', A: 4 };
  const rootNode = model.createRoot('bag', bag);

  const all = model.expand(rootNode.reference);
  assert.equal(all.total, 6);
  assert.deepEqual(
    all.nodes.map((n) => n.name),
    ['2', '10', 'A', 'a', 'b', 'c'],
  );

  const page = model.expand(rootNode.reference, { start: 1, count: 3 });
  assert.equal(page.total, 6);
  assert.deepEqual(
    page.nodes.map((n) => n.name),
    ['10', 'A', 'a'],
  );

  const tail = model.expand(rootNode.reference, { start: 4 });
  assert.deepEqual(
    tail.nodes.map((n) => n.name),
    ['b', 'c'],
  );

  const empty = model.expand(rootNode.reference, { start: 99, count: 5 });
  assert.deepEqual(empty.nodes, []);
  assert.equal(empty.total, 6);
});

test('compareKeys orders integer-like keys numerically before strings', () => {
  const keys = ['b', '10', '2', 'a', '1'];
  assert.deepEqual([...keys].sort(compareKeys), ['1', '2', '10', 'a', 'b']);
});

test('getters are inert by default and invoked on demand', () => {
  const model = new DebugVariableModel();
  const { root, getterCalls } = buildGraph();
  const rootNode = model.createRoot('root', root);

  const lazy = childByName(model.expand(rootNode.reference).nodes, 'secret');
  assert.equal(lazy.kind, 'getter');
  assert.equal(lazy.reference, 0);
  assert.equal(getterCalls(), 0);

  const eager = childByName(
    model.expand(rootNode.reference, { invokeGetters: true }).nodes,
    'secret',
  );
  assert.equal(eager.kind, 'primitive');
  assert.equal(eager.value, '42');
  assert.equal(getterCalls(), 1);
});

test('throwing getters are reported, not propagated', () => {
  const model = new DebugVariableModel();
  const obj = {};
  Object.defineProperty(obj, 'boom', {
    enumerable: true,
    get() {
      throw new Error('kaboom');
    },
  });
  const rootNode = model.createRoot('obj', obj);
  const node = childByName(
    model.expand(rootNode.reference, { invokeGetters: true }).nodes,
    'boom',
  );
  assert.equal(node.kind, 'getter');
  assert.match(node.value, /kaboom/);
});

test('primitives are leaves with reference 0', () => {
  const model = new DebugVariableModel();
  const rootNode = model.createRoot('obj', { n: 1, s: 'x', nil: null });
  const children = model.expand(rootNode.reference).nodes;
  for (const node of children) {
    assert.equal(node.kind, 'primitive');
    assert.equal(node.reference, 0);
    assert.equal(node.objectId, undefined);
  }
});
