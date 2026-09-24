import assert from "node:assert/strict";
import test from "node:test";
import { ShardedText } from "../src/index.ts";
import type { LeafNode, TextNode } from "../src/index.ts";

function collectLeaves(root: TextNode): LeafNode[] {
  const leaves: LeafNode[] = [];
  const walk = (node: TextNode): void => {
    if (node.kind === "leaf") {
      leaves.push(node);
      return;
    }
    walk(node.left);
    walk(node.right);
  };
  walk(root);
  return leaves;
}

function validateNode(node: TextNode): {
  length: number;
  breaks: number;
  height: number;
} {
  if (node.kind === "leaf") {
    let breaks = 0;
    for (const character of node.text) {
      if (character === "\n") breaks += 1;
    }
    assert.equal(node.length, node.text.length);
    assert.equal(node.breaks, breaks);
    assert.equal(node.height, 1);
    assert.ok(Object.isFrozen(node));
    return { length: node.length, breaks, height: 1 };
  }
  const left = validateNode(node.left);
  const right = validateNode(node.right);
  assert.equal(node.length, left.length + right.length);
  assert.equal(node.breaks, left.breaks + right.breaks);
  assert.equal(node.height, 1 + Math.max(left.height, right.height));
  assert.ok(
    Math.abs(left.height - right.height) <= 1,
    "tree must stay AVL balanced",
  );
  assert.ok(Object.isFrozen(node));
  return { length: node.length, breaks: node.breaks, height: node.height };
}

test("nodes cache UTF-16 length and line-break counts", () => {
  const document = ShardedText.from("😀\r\nx\n".repeat(40));
  assert.equal(document.length, 240);
  assert.equal(document.lineBreakCount, 80);
  assert.equal(document.lineCount, 81);
  assert.equal(document.root.kind, "branch");
  assert.equal(document.root.length, 240);
  assert.equal(document.root.breaks, 80);
  validateNode(document.root);

  for (const leaf of collectLeaves(document.root)) {
    assert.ok(leaf.length <= 130);
  }
});

test("three generations share unchanged nodes without read pollution", () => {
  const segment = "0123456789".repeat(20);
  const base = Array.from({ length: 8 }, (_, index) => `seg${index}:${segment}`)
    .join("\n");
  const v1 = ShardedText.from(base);
  const expectedV2 = `${base.slice(0, 5)}HELLO${base.slice(5)}`;
  const v2 = v1.insert(5, "HELLO");
  const expectedV3 = `${expectedV2.slice(0, -5)}WORLD`;
  const v3 = v2.replace(v2.length - 5, v2.length, "WORLD");

  assert.notEqual(v1.root, v2.root);
  assert.notEqual(v2.root, v3.root);

  const leaves1 = collectLeaves(v1.root);
  const leaves2 = collectLeaves(v2.root);
  const leaves3 = collectLeaves(v3.root);
  const leaves1ByIdentity = new Set<TextNode>(leaves1);
  assert.ok(
    leaves2.filter((leaf) => leaves1ByIdentity.has(leaf)).length >=
      leaves1.length - 4,
  );
  assert.ok(
    leaves3.filter((leaf) => leaves1ByIdentity.has(leaf)).length >=
      leaves1.length - 6,
  );

  assert.equal(v1.toString(), base);
  assert.equal(v2.toString(), expectedV2);
  assert.equal(v3.toString(), expectedV3);
  assert.equal(v1.slice(0, 100), base.slice(0, 100));
  assert.equal(v2.slice(100, 220), expectedV2.slice(100, 220));
  validateNode(v3.root);
});

test("small edits do not copy the whole document", () => {
  const base = Array.from(
    { length: 100 },
    (_, index) => `line-${index}-${"x".repeat(150)}`,
  ).join("\n");
  const v1 = ShardedText.from(base);
  const v2 = v1.insert(Math.floor(v1.length / 2), "!");
  const leaves1 = collectLeaves(v1.root);
  const leaves1ByIdentity = new Set<TextNode>(leaves1);
  const shared = collectLeaves(v2.root).filter((leaf) =>
    leaves1ByIdentity.has(leaf),
  );

  assert.ok(leaves1.length > 100);
  assert.ok(
    shared.length >= leaves1.length - 2,
    `only ${shared.length}/${leaves1.length} leaves were shared`,
  );
  assert.equal(v1.length, base.length);
  validateNode(v2.root);
});

test("range iteration touches only intersecting chunks", () => {
  const base = Array.from({ length: 20 }, (_, index) => {
    return `chunk-${index}-${"y".repeat(100)}`;
  }).join("|");
  const document = ShardedText.from(base);

  const single = [...document.readRange(10, 20)];
  assert.equal(single.length, 1);
  assert.deepEqual(single[0], {
    text: base.slice(10, 20),
    start: 10,
    end: 20,
  });

  const chunks = [...document.readRange(100, 500)];
  assert.ok(chunks.length > 1);
  assert.equal(
    chunks.map((chunk) => chunk.text).join(""),
    base.slice(100, 500),
  );
  assert.equal(chunks[0]?.start, 100);
  assert.equal(chunks.at(-1)?.end, 500);
  for (let index = 1; index < chunks.length; index += 1) {
    assert.equal(chunks[index]?.start, chunks[index - 1]?.end);
  }

  assert.equal(
    [...document.readRange(0, document.length)].map((chunk) => chunk.text)
      .join(""),
    base,
  );
  assert.deepEqual([...document.readRange(30, 30)], []);
  assert.throws(() => document.readRange(40, 10), /greater/);
});

test("versions stay immutable after further edits", () => {
  const v1 = ShardedText.from("one\ntwo\nthree");
  const snapshot = v1.toString();
  const v2 = v1.insert(0, "zero\n");
  const v3 = v2.delete(0, 5);

  assert.ok(Object.isFrozen(v1.root));
  assert.equal(v1.toString(), snapshot);
  assert.equal(v3.toString(), snapshot);
  assert.notEqual(v1.root, v2.root);
});

test("empty documents and trailing line breaks", () => {
  const empty = ShardedText.empty();
  assert.equal(empty.length, 0);
  assert.equal(empty.lineCount, 1);
  assert.deepEqual(empty.positionAt(0), { line: 0, column: 0 });
  assert.equal(empty.offsetAt({ line: 0, column: 0 }), 0);
  assert.deepEqual([...empty.readRange(0, 0)], []);
  assert.equal(ShardedText.from("").length, 0);

  const document = ShardedText.from("a\n");
  assert.equal(document.lineCount, 2);
  assert.deepEqual(document.positionAt(1), { line: 0, column: 1 });
  assert.deepEqual(document.positionAt(2), { line: 1, column: 0 });
  assert.equal(document.offsetAt({ line: 1, column: 0 }), 2);
  assert.equal(document.offsetAt({ line: 0, column: 1 }), 1);
  assert.throws(() => document.offsetAt({ line: 2, column: 0 }), /outside/);
  assert.throws(() => document.offsetAt({ line: 0, column: 2 }), /beyond/);
});
