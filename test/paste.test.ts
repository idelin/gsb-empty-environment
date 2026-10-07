import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Graph, type CanvasNode } from '../src/graph.ts';
import { IdAllocator } from '../src/id-allocator.ts';
import { pasteSubgraph, PasteError } from '../src/paste.ts';
import { isIsomorphicPaste } from '../src/isomorphism.ts';

function node(
  id: string,
  type: string,
  refs: CanvasNode['refs'] = [],
): CanvasNode {
  return { id, type, refs };
}

test('paste assigns fresh public ids to every copied node', () => {
  const graph = new Graph();
  const allocator = new IdAllocator();
  const clipboard = [node('a', 'box'), node('b', 'box')];

  const result = pasteSubgraph(clipboard, graph, allocator);

  assert.deepEqual(
    result.nodes.map((n) => n.id),
    ['n_000000', 'n_000001'],
  );
  assert.equal(allocator.cursor, 2);
  assert.ok(graph.has('n_000000'));
  assert.ok(graph.has('n_000001'));
  assert.ok(!graph.has('a'));
});

test('internal references are rewired to the new nodes', () => {
  const graph = new Graph();
  const allocator = new IdAllocator();
  const clipboard = [
    node('a', 'box', [
      { kind: 'ref', key: 'child', targetId: 'b', targetType: 'box' },
    ]),
    node('b', 'box', [
      { kind: 'ref', key: 'parent', targetId: 'a', targetType: 'box' },
    ]),
  ];

  const result = pasteSubgraph(clipboard, graph, allocator);
  const [a, b] = result.nodes;

  assert.deepEqual(a!.refs, [
    { kind: 'ref', key: 'child', targetId: b!.id, targetType: 'box' },
  ]);
  assert.deepEqual(b!.refs, [
    { kind: 'ref', key: 'parent', targetId: a!.id, targetType: 'box' },
  ]);
  assert.equal(result.brokenRefs.length, 0);
  assert.ok(isIsomorphicPaste(clipboard, result.nodes, result.idMap));
});

test('external reference is kept when target exists and type matches', () => {
  const graph = new Graph();
  graph.add(node('ext', 'port'));
  const allocator = new IdAllocator();
  const clipboard = [
    node('a', 'box', [
      { kind: 'ref', key: 'out', targetId: 'ext', targetType: 'port' },
    ]),
  ];

  const result = pasteSubgraph(clipboard, graph, allocator);

  assert.deepEqual(result.nodes[0]!.refs, [
    { kind: 'ref', key: 'out', targetId: 'ext', targetType: 'port' },
  ]);
  assert.equal(result.brokenRefs.length, 0);
  assert.ok(isIsomorphicPaste(clipboard, result.nodes, result.idMap));
});

test('external reference to a missing target is marked dangling', () => {
  const graph = new Graph();
  const allocator = new IdAllocator();
  const clipboard = [
    node('a', 'box', [
      { kind: 'ref', key: 'out', targetId: 'ghost', targetType: 'port' },
    ]),
  ];

  const result = pasteSubgraph(clipboard, graph, allocator);

  assert.deepEqual(result.nodes[0]!.refs, [
    {
      kind: 'dangling',
      key: 'out',
      originalTargetId: 'ghost',
      targetType: 'port',
      reason: 'missing-target',
    },
  ]);
  assert.deepEqual(result.brokenRefs, [
    {
      nodeId: 'n_000000',
      key: 'out',
      originalTargetId: 'ghost',
      reason: 'missing-target',
    },
  ]);
  assert.ok(isIsomorphicPaste(clipboard, result.nodes, result.idMap));
});

test('external reference with a type mismatch is marked dangling', () => {
  const graph = new Graph();
  graph.add(node('ext', 'wrong-type'));
  const allocator = new IdAllocator();
  const clipboard = [
    node('a', 'box', [
      { kind: 'ref', key: 'out', targetId: 'ext', targetType: 'port' },
    ]),
  ];

  const result = pasteSubgraph(clipboard, graph, allocator);

  assert.equal(result.nodes[0]!.refs[0]!.kind, 'dangling');
  assert.equal(result.brokenRefs[0]!.reason, 'type-mismatch');
  assert.ok(isIsomorphicPaste(clipboard, result.nodes, result.idMap));
});

test('mixed internal/external/dangling refs in one paste', () => {
  const graph = new Graph();
  graph.add(node('ext', 'port'));
  const allocator = new IdAllocator();
  const clipboard = [
    node('a', 'box', [
      { kind: 'ref', key: 'child', targetId: 'b', targetType: 'box' },
      { kind: 'ref', key: 'out', targetId: 'ext', targetType: 'port' },
      { kind: 'ref', key: 'lost', targetId: 'gone', targetType: 'port' },
    ]),
    node('b', 'box'),
  ];

  const result = pasteSubgraph(clipboard, graph, allocator);
  const [a, b] = result.nodes;

  assert.deepEqual(a!.refs[0], {
    kind: 'ref',
    key: 'child',
    targetId: b!.id,
    targetType: 'box',
  });
  assert.deepEqual(a!.refs[1], {
    kind: 'ref',
    key: 'out',
    targetId: 'ext',
    targetType: 'port',
  });
  assert.equal(a!.refs[2]!.kind, 'dangling');
  assert.equal(result.brokenRefs.length, 1);
  assert.ok(isIsomorphicPaste(clipboard, result.nodes, result.idMap));
});

test('failed paste does not consume the public id sequence', () => {
  const graph = new Graph();
  const allocator = new IdAllocator();

  // One successful paste to move the cursor off zero.
  pasteSubgraph([node('x', 'box')], graph, allocator);
  assert.equal(allocator.cursor, 1);

  // Empty clipboard: rejected before any allocation.
  assert.throws(() => pasteSubgraph([], graph, allocator), PasteError);
  assert.equal(allocator.cursor, 1);

  // Duplicate ids in clipboard: rejected before any allocation.
  assert.throws(
    () => pasteSubgraph([node('x', 'box'), node('x', 'box')], graph, allocator),
    PasteError,
  );
  assert.equal(allocator.cursor, 1);

  // Next successful paste continues the sequence with no gap.
  const result = pasteSubgraph([node('y', 'box')], graph, allocator);
  assert.equal(result.nodes[0]!.id, 'n_000001');
  assert.equal(allocator.cursor, 2);
});

test('id conflict with the graph rolls back ids and leaves graph untouched', () => {
  const graph = new Graph();
  const allocator = new IdAllocator();
  // Force the allocator to hand out an id that already exists in the graph.
  graph.add(node('n_000000', 'box'));

  const before = graph.snapshot();
  assert.throws(() => pasteSubgraph([node('a', 'box')], graph, allocator));

  assert.equal(allocator.cursor, 0);
  assert.equal(graph.size, before.size);
  assert.ok(graph.has('n_000000'));
  assert.ok(!graph.has('a'));
});

test('consecutive pastes of the same clipboard get disjoint ids', () => {
  const graph = new Graph();
  const allocator = new IdAllocator();
  const clipboard = [
    node('a', 'box', [
      { kind: 'ref', key: 'child', targetId: 'b', targetType: 'box' },
    ]),
    node('b', 'box'),
  ];

  const first = pasteSubgraph(clipboard, graph, allocator);
  const second = pasteSubgraph(clipboard, graph, allocator);

  const firstIds = new Set(first.nodes.map((n) => n.id));
  for (const n of second.nodes) {
    assert.ok(!firstIds.has(n.id));
  }
  assert.ok(isIsomorphicPaste(clipboard, first.nodes, first.idMap));
  assert.ok(isIsomorphicPaste(clipboard, second.nodes, second.idMap));
});
