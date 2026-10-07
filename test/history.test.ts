import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Graph, type CanvasNode } from '../src/graph.ts';
import { IdAllocator } from '../src/id-allocator.ts';
import { History, PasteCommand } from '../src/history.ts';
import { isIsomorphicPaste } from '../src/isomorphism.ts';

function node(
  id: string,
  type: string,
  refs: CanvasNode['refs'] = [],
): CanvasNode {
  return { id, type, refs };
}

function graphState(graph: Graph): string {
  return JSON.stringify(
    [...graph.values()].sort((x, y) => x.id.localeCompare(y.id)),
  );
}

const clipboard: CanvasNode[] = [
  node('a', 'box', [
    { kind: 'ref', key: 'child', targetId: 'b', targetType: 'box' },
    { kind: 'ref', key: 'out', targetId: 'ext', targetType: 'port' },
  ]),
  node('b', 'box'),
];

test('undo removes exactly the pasted nodes', () => {
  const graph = new Graph();
  graph.add(node('ext', 'port'));
  const allocator = new IdAllocator();
  const history = new History();
  const before = graphState(graph);

  const command = new PasteCommand(clipboard, graph, allocator);
  history.execute(command);
  assert.equal(graph.size, 3);
  assert.ok(history.canUndo);

  history.undo();
  assert.equal(graphState(graph), before);
  assert.ok(!history.canUndo);
  assert.ok(history.canRedo);
});

test('redo restores the identical state without consuming new ids', () => {
  const graph = new Graph();
  graph.add(node('ext', 'port'));
  const allocator = new IdAllocator();
  const history = new History();

  history.execute(new PasteCommand(clipboard, graph, allocator));
  history.execute(new PasteCommand(clipboard, graph, allocator));

  const cursorAfterPastes = allocator.cursor;
  const stateAfterPastes = graphState(graph);

  history.undo();
  history.undo();
  assert.equal(graph.size, 1);

  history.redo();
  history.redo();
  assert.equal(graphState(graph), stateAfterPastes);
  assert.equal(allocator.cursor, cursorAfterPastes);
});

test('undo/redo preserves rewired internal refs and kept external refs', () => {
  const graph = new Graph();
  graph.add(node('ext', 'port'));
  const allocator = new IdAllocator();
  const history = new History();

  const command = new PasteCommand(clipboard, graph, allocator);
  history.execute(command);
  const pasted = command.pastedIds.map((id) => graph.get(id)!);
  const idMap = new Map(clipboard.map((n, i) => [n.id, command.pastedIds[i]!]));
  assert.ok(isIsomorphicPaste(clipboard, pasted, idMap));

  history.undo();
  history.redo();
  const restored = command.pastedIds.map((id) => graph.get(id)!);
  assert.deepEqual(restored, pasted);
  assert.ok(isIsomorphicPaste(clipboard, restored, idMap));
});

test('a failed paste pushes nothing onto the undo stack', () => {
  const graph = new Graph();
  const allocator = new IdAllocator();
  const history = new History();

  assert.throws(() =>
    history.execute(new PasteCommand([], graph, allocator)),
  );
  assert.ok(!history.canUndo);
  assert.equal(allocator.cursor, 0);
});

test('executing a new command clears the redo stack', () => {
  const graph = new Graph();
  const allocator = new IdAllocator();
  const history = new History();

  history.execute(new PasteCommand([node('a', 'box')], graph, allocator));
  history.undo();
  assert.ok(history.canRedo);

  history.execute(new PasteCommand([node('b', 'box')], graph, allocator));
  assert.ok(!history.canRedo);
  assert.equal(allocator.cursor, 2);
});
