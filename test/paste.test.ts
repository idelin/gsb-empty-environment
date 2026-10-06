import { test } from "node:test";
import assert from "node:assert/strict";
import { Canvas } from "../src/model.ts";
import { IdAllocator } from "../src/ids.ts";
import { copy, paste, PasteError } from "../src/clipboard.ts";
import { History, PasteCommand } from "../src/history.ts";
import { assertIsomorphic, node } from "./helpers.ts";

function makeCanvas() {
  // a -> b is an internal pair; ext is an external "port" node.
  const canvas = new Canvas();
  canvas.addNode(node("a", "task", [{ key: "next", expectedType: "task", target: "b" }], { title: "A" }));
  canvas.addNode(
    node("b", "task", [
      { key: "prev", expectedType: "task", target: "a" },
      { key: "out", expectedType: "port", target: "ext" },
    ]),
  );
  canvas.addNode(node("ext", "port"));
  return canvas;
}

test("paste assigns fresh public ids to every copied node", () => {
  const canvas = makeCanvas();
  const allocator = new IdAllocator();
  const before = allocator.next;

  const result = paste(canvas, copy(canvas, ["a", "b"]), allocator);

  assert.equal(result.nodes.length, 2);
  assert.deepEqual(result.nodes.map((n) => n.id), ["node-1", "node-2"]);
  assert.equal(allocator.next, before + 2, "public sequence advanced by exactly the copied count");
  assert.ok(canvas.has("node-1") && canvas.has("node-2"));
  // originals untouched
  assert.equal(canvas.get("a")!.refs[0]!.target, "b");
});

test("internal references are remapped and the pasted subgraph is isomorphic", () => {
  const canvas = makeCanvas();
  const allocator = new IdAllocator();
  const clipboard = copy(canvas, ["a", "b"]);

  const result = paste(canvas, clipboard, allocator);

  assertIsomorphic(clipboard.nodes, result.nodes, result.idMap);
  const [na, nb] = result.nodes;
  assert.equal(na!.refs[0]!.target, nb!.id);
  assert.equal(nb!.refs.find((r) => r.key === "prev")!.target, na!.id);
});

test("external reference is kept when the target exists and the type matches", () => {
  const canvas = makeCanvas();
  const result = paste(canvas, copy(canvas, ["a", "b"]), new IdAllocator());

  const out = result.nodes[1]!.refs.find((r) => r.key === "out")!;
  assert.equal(out.status, "ok");
  assert.equal(out.target, "ext");
});

test("external reference to a missing target is marked broken", () => {
  const canvas = makeCanvas();
  canvas.removeNode("ext");
  const result = paste(canvas, copy(canvas, ["a", "b"]), new IdAllocator());

  const out = result.nodes[1]!.refs.find((r) => r.key === "out")!;
  assert.equal(out.status, "broken");
  assert.equal(out.target, null);
  assert.equal(out.brokenTarget, "ext");
});

test("external reference to a target of the wrong type is marked broken", () => {
  const canvas = makeCanvas();
  canvas.removeNode("ext");
  canvas.addNode(node("ext", "sink")); // same id, different type
  const result = paste(canvas, copy(canvas, ["a", "b"]), new IdAllocator());

  const out = result.nodes[1]!.refs.find((r) => r.key === "out")!;
  assert.equal(out.status, "broken");
  assert.equal(out.target, null);
});

test("a failed paste is atomic and does not consume public ids", () => {
  const canvas = makeCanvas();
  const allocator = new IdAllocator();
  const before = allocator.next;
  const idsBefore = canvas.ids();

  // capacity forces the paste to fail midway
  const tight = new Canvas(4);
  for (const id of canvas.ids()) {
    tight.addNode(canvas.get(id)!);
  }

  assert.throws(() => paste(tight, copy(tight, ["a", "b"]), allocator), PasteError);
  assert.equal(allocator.next, before, "public id sequence must not be consumed by a failed paste");
  assert.deepEqual(tight.ids(), idsBefore, "canvas must be restored after a failed paste");
});

test("pasting an empty clipboard fails without consuming ids", () => {
  const canvas = makeCanvas();
  const allocator = new IdAllocator();
  assert.throws(() => paste(canvas, { nodes: [] }, allocator), PasteError);
  assert.equal(allocator.next, 1);
});

test("repeated pastes draw disjoint id ranges", () => {
  const canvas = makeCanvas();
  const allocator = new IdAllocator();
  const clipboard = copy(canvas, ["a", "b"]);

  const first = paste(canvas, clipboard, allocator);
  const second = paste(canvas, clipboard, allocator);

  const ids = [...first.idMap.values(), ...second.idMap.values()];
  assert.equal(new Set(ids).size, ids.length);
  assert.deepEqual([...second.idMap.values()], ["node-3", "node-4"]);
});

test("undo removes the paste, redo restores it with the same ids", () => {
  const canvas = makeCanvas();
  const allocator = new IdAllocator();
  const history = new History();
  const clipboard = copy(canvas, ["a", "b"]);
  const command = new PasteCommand(canvas, clipboard, allocator);

  history.do(command);
  const pastedIds = command.result!.nodes.map((n) => n.id);
  assert.deepEqual(pastedIds, ["node-1", "node-2"]);
  assertIsomorphic(clipboard.nodes, command.result!.nodes, command.result!.idMap);

  history.undo();
  assert.ok(!canvas.has("node-1") && !canvas.has("node-2"));
  assert.deepEqual(canvas.ids(), ["a", "b", "ext"]);

  history.redo();
  assert.ok(canvas.has("node-1") && canvas.has("node-2"));
  assert.equal(allocator.next, 3, "redo must not consume additional public ids");
  assertIsomorphic(clipboard.nodes, command.result!.nodes, command.result!.idMap);

  // a second undo/redo cycle is still stable
  history.undo();
  history.redo();
  assert.equal(allocator.next, 3);
  assertIsomorphic(clipboard.nodes, command.result!.nodes, command.result!.idMap);
});

test("undo/redo interleaved with a fresh paste keeps ids monotonic", () => {
  const canvas = makeCanvas();
  const allocator = new IdAllocator();
  const history = new History();
  const clipboard = copy(canvas, ["a", "b"]);

  const first = new PasteCommand(canvas, clipboard, allocator);
  history.do(first); // node-1, node-2
  history.undo();

  const second = new PasteCommand(canvas, clipboard, allocator);
  history.do(second); // node-3, node-4 (redo stack cleared)
  assert.deepEqual([...second.result!.idMap.values()], ["node-3", "node-4"]);

  history.undo();
  history.redo();
  assert.equal(allocator.next, 5, "undo/redo never replays the public sequence");
  assertIsomorphic(clipboard.nodes, second.result!.nodes, second.result!.idMap);
});
