import { test } from 'node:test';
import assert from 'node:assert/strict';

import { VirtualGraph } from '../src/virtualGraph.ts';
import {
  DebugModel,
  StaleHandleError,
  UnknownHandleError,
  comparePropertyKeys,
} from '../src/model.ts';

function entry(page: { items: { name: string }[] }, name: string) {
  const found = page.items.find((item) => item.name === name);
  assert.ok(found, `expected property ${name} in page`);
  return found;
}

test('shared objects keep one identity and are not copied', () => {
  const graph = new VirtualGraph();
  const shared = graph.node('Point').set('x', 1).set('y', 2);
  const root = graph.node('Shape').set('from', shared).set('to', shared);

  const model = new DebugModel();
  const [rootHandle] = model.pause([root]);

  const page = model.expand(rootHandle.id);
  const from = entry(page, 'from');
  const to = entry(page, 'to');

  // Both paths reference the same object: identical ref id, no copies.
  assert.ok(from.refId);
  assert.equal(from.refId, to.refId);

  // Expanding through either path yields the same children with same ids.
  const viaFrom = model.expand(from.refId!);
  const viaTo = model.expand(to.refId!);
  assert.deepEqual(viaFrom, viaTo);
  assert.equal(viaFrom.total, 2);
});

test('duplicate roots intern to the same handle id', () => {
  const graph = new VirtualGraph();
  const root = graph.node();

  const model = new DebugModel();
  const [a, b] = model.pause([root, root]);
  assert.equal(a.id, b.id);
});

test('self cycles resolve back to the same handle id', () => {
  const graph = new VirtualGraph();
  const node = graph.node('Chain');
  node.set('self', node);

  const model = new DebugModel();
  const [handle] = model.pause([node]);

  const page = model.expand(handle.id);
  assert.equal(entry(page, 'self').refId, handle.id);
});

test('mutual cycles expand lazily without unrolling the graph', () => {
  const graph = new VirtualGraph();
  const a = graph.node('A');
  const b = graph.node('B');
  a.set('b', b);
  b.set('a', a);

  const model = new DebugModel();
  const [handleA] = model.pause([a]);

  const pageA = model.expand(handleA.id);
  const refB = entry(pageA, 'b').refId!;
  assert.notEqual(refB, handleA.id);

  const pageB = model.expand(refB);
  // Walking the cycle leads back to the very same handle id for A.
  assert.equal(entry(pageB, 'a').refId, handleA.id);
});

test('a new pause generation invalidates old handles', () => {
  const graph = new VirtualGraph();
  const first = graph.node('First');
  const second = graph.node('Second');

  const model = new DebugModel();
  const [oldHandle] = model.pause([first]);
  assert.ok(model.isValid(oldHandle.id));
  model.expand(oldHandle.id);

  const [newHandle] = model.pause([second]);
  assert.equal(newHandle.generation, oldHandle.generation + 1);

  assert.ok(!model.isValid(oldHandle.id));
  assert.throws(() => model.expand(oldHandle.id), StaleHandleError);
  assert.throws(() => model.evaluateGetter(oldHandle.id, 'x'), StaleHandleError);
  assert.ok(model.isValid(newHandle.id));
});

test('resume invalidates outstanding handles', () => {
  const graph = new VirtualGraph();
  const root = graph.node();

  const model = new DebugModel();
  const [handle] = model.pause([root]);
  model.resume();

  assert.ok(!model.isValid(handle.id));
  assert.throws(() => model.expand(handle.id), StaleHandleError);
});

test('re-issued generations can reuse ids but stay consistent', () => {
  const graph = new VirtualGraph();
  const root = graph.node('Root').set('k', 1);

  const model = new DebugModel();
  const [first] = model.pause([root]);
  const [second] = model.pause([root]);

  // Same object, new generation: ids may collide textually, but only the
  // current generation resolves.
  assert.equal(second.generation, first.generation + 1);
  assert.ok(model.isValid(second.id));
  if (first.id === second.id) {
    assert.doesNotThrow(() => model.expand(second.id));
  } else {
    assert.throws(() => model.expand(first.id), StaleHandleError);
  }
});

test('children are sorted by key: numeric first, then strings', () => {
  const graph = new VirtualGraph();
  const root = graph
    .node()
    .set('b', 1)
    .set('10', 2)
    .set('2', 3)
    .set('a', 4)
    .set('1', 5)
    .set('30', 6);

  const model = new DebugModel();
  const [handle] = model.pause([root]);
  const page = model.expand(handle.id);

  assert.deepEqual(
    page.items.map((item) => item.name),
    ['1', '2', '10', '30', 'a', 'b'],
  );
});

test('paging walks the sorted key order in stable slices', () => {
  const graph = new VirtualGraph();
  const root = graph.node();
  for (const name of ['c', 'a', 'e', 'b', 'd']) root.set(name, name);

  const model = new DebugModel();
  const [handle] = model.pause([root]);

  const seen: string[] = [];
  let offset = 0;
  let pages = 0;
  for (;;) {
    const page = model.expand(handle.id, { offset, count: 2 });
    pages += 1;
    assert.equal(page.total, 5);
    assert.equal(page.offset, offset);
    seen.push(...page.items.map((item) => item.name));
    offset += page.items.length;
    if (!page.hasMore) break;
    assert.ok(pages < 10, 'paging must terminate');
  }

  assert.deepEqual(seen, ['a', 'b', 'c', 'd', 'e']);
  assert.equal(pages, 3);

  const beyond = model.expand(handle.id, { offset: 99, count: 2 });
  assert.deepEqual(beyond.items, []);
  assert.equal(beyond.hasMore, false);
});

test('getters are not invoked by default', () => {
  const graph = new VirtualGraph();
  const root = graph.node('Lazy');
  graph.trackedAccessor(root, 'expensive', () => 42);
  root.set('cheap', 7);

  const model = new DebugModel();
  const [handle] = model.pause([root]);

  const page = model.expand(handle.id);
  assert.equal(graph.getterCalls, 0);

  const lazy = entry(page, 'expensive');
  assert.equal(lazy.kind, 'accessor');
  assert.equal(lazy.getterInvoked, false);
  assert.equal(lazy.refId, null);
  assert.equal(entry(page, 'cheap').preview, '7');
});

test('getters run only on explicit request', () => {
  const graph = new VirtualGraph();
  const target = graph.node('Target');
  const root = graph.node('Lazy');
  graph.trackedAccessor(root, 'obj', () => target);
  graph.trackedAccessor(root, 'num', () => 42);

  const model = new DebugModel();
  const [handle] = model.pause([root]);

  const single = model.evaluateGetter(handle.id, 'num');
  assert.equal(single.preview, '42');
  assert.equal(single.getterInvoked, true);
  assert.equal(graph.getterCalls, 1);

  const page = model.expand(handle.id, { invokeGetters: true });
  assert.equal(graph.getterCalls, 3);
  const obj = entry(page, 'obj');
  assert.ok(obj.refId);
  assert.equal(obj.preview, target.className + '#' + target.nodeId);
});

test('getter results that are objects get normal handles', () => {
  const graph = new VirtualGraph();
  const shared = graph.node('Shared');
  const root = graph.node('Lazy');
  root.set('direct', shared);
  graph.trackedAccessor(root, 'viaGetter', () => shared);

  const model = new DebugModel();
  const [handle] = model.pause([root]);

  const page = model.expand(handle.id, { invokeGetters: true });
  // Identity holds even when one reference surfaces through a getter.
  assert.equal(entry(page, 'direct').refId, entry(page, 'viaGetter').refId);
});

test('malformed and unknown handles are rejected distinctly', () => {
  const graph = new VirtualGraph();
  const model = new DebugModel();
  model.pause([graph.node()]);

  assert.throws(() => model.expand('not-a-handle'), UnknownHandleError);
  assert.throws(() => model.expand('obj@1:999'), UnknownHandleError);
  assert.ok(!model.isValid('not-a-handle'));
});

test('comparePropertyKeys orders indices before strings', () => {
  const keys = ['x', '03', '3', '0', 'A', 'a', '10'];
  const sorted = [...keys].sort(comparePropertyKeys);
  assert.deepEqual(sorted, ['0', '3', '10', '03', 'A', 'a', 'x']);
});
