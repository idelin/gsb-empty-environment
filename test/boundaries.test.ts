import assert from "node:assert/strict";
import test from "node:test";
import { ShardedText } from "../src/index.ts";
import type { TextNode } from "../src/index.ts";

function validateTree(node: TextNode): { length: number; breaks: number } {
  if (node.kind === "leaf") {
    let breaks = 0;
    for (const character of node.text) {
      if (character === "\n") breaks += 1;
    }
    assert.equal(node.length, node.text.length);
    assert.equal(node.breaks, breaks);
    assert.equal(node.height, 1);
    return { length: node.length, breaks };
  }
  const left = validateTree(node.left);
  const right = validateTree(node.right);
  assert.equal(node.length, left.length + right.length);
  assert.equal(node.breaks, left.breaks + right.breaks);
  assert.equal(node.height, 1 + Math.max(left.height, right.height));
  assert.ok(Math.abs(node.left.height - node.right.height) <= 1);
  return { length: node.length, breaks: node.breaks };
}

test("LF and CRLF both support line and column mapping", () => {
  const document = ShardedText.from("alpha\nbeta\r\ngamma");
  assert.equal(document.lineCount, 3);
  assert.deepEqual(document.positionAt(6), { line: 1, column: 0 });
  assert.deepEqual(document.positionAt(10), { line: 1, column: 4 });
  assert.deepEqual(document.positionAt(12), { line: 2, column: 0 });
  assert.equal(document.offsetAt({ line: 1, column: 0 }), 6);
  assert.equal(document.offsetAt({ line: 1, column: 4 }), 10);
  assert.equal(document.offsetAt({ line: 2, column: 0 }), 12);
  assert.equal(document.offsetAt({ line: 2, column: 5 }), 17);

  assert.throws(() => document.positionAt(11), /CRLF/);
  assert.throws(() => document.offsetAt({ line: 1, column: 5 }), /beyond/);
});

test("edit boundaries inside CRLF are rejected", () => {
  const document = ShardedText.from("a\r\nb");
  assert.throws(() => document.insert(2, "x"), /CRLF/);
  assert.throws(() => document.delete(1, 2), /CRLF/);
  assert.throws(() => document.delete(2, 3), /CRLF/);
  assert.throws(() => document.replace(0, 2, "x"), /CRLF/);
  assert.throws(() => document.replace(2, 3, "x"), /CRLF/);
  assert.throws(() => [...document.readRange(0, 2)], /CRLF/);

  assert.equal(document.delete(1, 3).toString(), "ab");
  assert.equal(document.replace(1, 3, "|").toString(), "a|b");

  const inserted = ShardedText.from("ab").insert(1, "\r\n");
  assert.equal(inserted.toString(), "a\r\nb");
  assert.equal(inserted.lineCount, 2);
  assert.equal(inserted.offsetAt({ line: 1, column: 0 }), 3);
});

test("lone CR input is rejected for LF and CRLF coverage", () => {
  assert.throws(() => ShardedText.from("a\rb"), /Lone CR/);
  assert.throws(() => ShardedText.from("a\r"), /Lone CR/);
  assert.throws(() => ShardedText.from("ab").insert(1, "\r"), /Lone CR/);
  assert.throws(() => ShardedText.from("ab").insert(1, "x\ry"), /Lone CR/);
});

test("UTF-16 offsets never split surrogate pairs", () => {
  const document = ShardedText.from("a😀b");
  assert.equal(document.length, 4);
  assert.throws(() => document.insert(2, "x"), /surrogate/);
  assert.throws(() => document.delete(1, 2), /surrogate/);
  assert.throws(() => document.delete(2, 3), /surrogate/);
  assert.throws(() => document.replace(2, 3, "x"), /surrogate/);
  assert.equal(document.delete(1, 3).toString(), "ab");

  assert.throws(() => ShardedText.from("\ud800"), /unpaired/);
  assert.throws(() => ShardedText.from("\udfff"), /unpaired/);
  assert.throws(() => document.insert(0, "\ud800"), /unpaired/);
});

test("emoji positions are reversible with UTF-16 columns", () => {
  const document = ShardedText.from("😀x\n🚀");
  assert.equal(document.length, 6);
  assert.deepEqual(document.positionAt(2), { line: 0, column: 2 });
  assert.equal(document.offsetAt({ line: 0, column: 2 }), 2);
  assert.deepEqual(document.positionAt(4), { line: 1, column: 0 });
  assert.equal(document.offsetAt({ line: 1, column: 0 }), 4);
  assert.throws(() => document.positionAt(1), /surrogate/);
  assert.throws(() => document.offsetAt({ line: 0, column: 1 }), /surrogate/);
  assert.throws(() => document.positionAt(5), /surrogate/);

  for (let offset = 0; offset <= document.length; offset += 1) {
    let position;
    try {
      position = document.positionAt(offset);
    } catch {
      continue;
    }
    assert.equal(document.offsetAt(position), offset);
  }
});

test("every valid offset round-trips in a mixed document", () => {
  const text = "😀a\r\nb🚀\n\nx\r\n";
  const document = ShardedText.from(text);
  let invalidOffsets = 0;
  for (let offset = 0; offset <= document.length; offset += 1) {
    try {
      const position = document.positionAt(offset);
      assert.equal(document.offsetAt(position), offset);
    } catch {
      invalidOffsets += 1;
    }
  }
  assert.ok(invalidOffsets > 0);
});

function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state = (state + 0x6d2b79f5) | 0;
    let value = Math.imul(state ^ (state >>> 15), 1 | state);
    value = (value + Math.imul(value ^ (value >>> 7), 61 | value)) ^ value;
    return ((value ^ (value >>> 14)) >>> 0) / 4294967296;
  };
}

function validOffsets(text: string): number[] {
  const offsets: number[] = [];
  for (let offset = 0; offset <= text.length; offset += 1) {
    if (offset > 0 && offset < text.length) {
      const previous = text.charCodeAt(offset - 1);
      const current = text.charCodeAt(offset);
      const high = previous >= 0xd800 && previous <= 0xdbff;
      const low = current >= 0xdc00 && current <= 0xdfff;
      if ((high && low) || (previous === 0x0d && current === 0x0a)) continue;
    }
    offsets.push(offset);
  }
  return offsets;
}

test("random edits match a string reference model", () => {
  const random = mulberry32(20260924);
  const pieces = ["a", "bc", "\n", "\r\n", "😀", "x🚀y", " ", "Ω"];
  let model = "start\r\n😀 line\nend";
  let document = ShardedText.from(model);

  for (let iteration = 0; iteration < 300; iteration += 1) {
    const offsets = validOffsets(model);
    const choice = random();
    const insertText = Array.from(
      { length: 1 + Math.floor(random() * 3) },
      () => pieces[Math.floor(random() * pieces.length)],
    ).join("");

    if (choice < 0.45) {
      const offset = offsets[Math.floor(random() * offsets.length)] as number;
      document = document.insert(offset, insertText);
      model = model.slice(0, offset) + insertText + model.slice(offset);
    } else if (choice < 0.75 && offsets.length >= 3) {
      const firstCandidates = offsets.slice(0, -1);
      const first =
        firstCandidates[Math.floor(random() * firstCandidates.length)] as number;
      const later = offsets.filter((offset) => offset > first);
      const second = later[Math.floor(random() * later.length)] as number;
      document = document.delete(first, second);
      model = model.slice(0, first) + model.slice(second);
    } else {
      const first = offsets[Math.floor(random() * offsets.length)] as number;
      const later = offsets.filter((offset) => offset >= first);
      const second = later[Math.floor(random() * later.length)] as number;
      document = document.replace(first, second, insertText);
      model = model.slice(0, first) + insertText + model.slice(second);
    }

    assert.equal(document.toString(), model);
    assert.equal(document.length, model.length);
    assert.equal(
      document.lineCount,
      [...model].filter((character) => character === "\n").length + 1,
    );
    validateTree(document.root);

    if (iteration % 25 === 0) {
      const safe = validOffsets(model);
      const offset = safe[Math.floor(random() * safe.length)] as number;
      assert.equal(document.offsetAt(document.positionAt(offset)), offset);
    }
  }
});
