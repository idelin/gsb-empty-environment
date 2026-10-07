import { test } from "node:test";
import assert from "node:assert/strict";
import { TransactionalEditor } from "../src/editor.ts";
import { edit } from "../src/types.ts";

test("a transaction applies multiple edits atomically", () => {
  const editor = new TransactionalEditor("foo bar baz");
  const tx = editor.transact([edit(0, 3, "one"), edit(8, 11, "three")]);
  assert.equal(editor.getText(), "one bar three");
  assert.equal(tx.version, 2); // one version step per edit
  assert.equal(editor.version, 2);
});

test("undo restores the pre-transaction text in one step", () => {
  const editor = new TransactionalEditor("foo bar baz");
  editor.transact([edit(0, 3, "one"), edit(8, 11, "three")]);
  assert.ok(editor.canUndo);
  assert.ok(editor.undo());
  assert.equal(editor.getText(), "foo bar baz");
  assert.ok(!editor.canUndo);
});

test("redo reapplies the whole transaction", () => {
  const editor = new TransactionalEditor("foo bar baz");
  editor.transact([edit(0, 3, "one"), edit(8, 11, "three")]);
  editor.undo();
  assert.ok(editor.redo());
  assert.equal(editor.getText(), "one bar three");
});

test("undo is itself recorded in the chain: versions never go backwards", () => {
  const editor = new TransactionalEditor("ab");
  editor.transact([edit(0, 1, "A")]); // v1
  editor.undo(); // v2 (inverse edit appended)
  assert.equal(editor.version, 2);
  assert.equal(editor.getText(), "ab");
  // the chain still explains how to get from v0 to the current text
  assert.equal(editor.document.chain.currentVersion, 2);
});

test("a new transaction clears the redo stack", () => {
  const editor = new TransactionalEditor("ab");
  editor.transact([edit(0, 1, "A")]);
  editor.undo();
  editor.transact([edit(1, 2, "B")]);
  assert.ok(!editor.canRedo);
  assert.equal(editor.getText(), "aB");
});

test("overlapping edits in one transaction are rejected", () => {
  const editor = new TransactionalEditor("abcdef");
  assert.throws(() => editor.transact([edit(0, 3, "x"), edit(2, 5, "y")]), /overlap/);
  assert.equal(editor.getText(), "abcdef");
});

test("undo of a transaction that followed other edits restores correctly", () => {
  const editor = new TransactionalEditor("hello world");
  editor.transact([edit(0, 5, "goodbye")]); // "goodbye world"
  editor.transact([edit(8, 13, "there")]); // "goodbye there"
  assert.equal(editor.getText(), "goodbye there");
  editor.undo();
  assert.equal(editor.getText(), "goodbye world");
  editor.undo();
  assert.equal(editor.getText(), "hello world");
});

test("undo/redo with mixed-length edits in one transaction (inverse coordinates)", () => {
  const editor = new TransactionalEditor("aa bb cc");
  // unequal lengths on both sides exercise inverse position shifting
  editor.transact([edit(0, 2, "aaaaa"), edit(6, 8, "c")]);
  assert.equal(editor.getText(), "aaaaa bb c");
  editor.undo();
  assert.equal(editor.getText(), "aa bb cc");
  editor.redo();
  assert.equal(editor.getText(), "aaaaa bb c");
  editor.undo();
  assert.equal(editor.getText(), "aa bb cc");
});
