import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CompletionSession,
  StaleCompletionError,
  type CompletionItem,
  type CompletionResponse,
} from "../src/completion.ts";
import { TransactionalEditor } from "../src/editor.ts";
import { edit, range } from "../src/types.ts";

function makeSession(text: string, response: CompletionResponse) {
  const editor = new TransactionalEditor(text);
  const session = new CompletionSession(editor, response);
  return { editor, session };
}

const fooItems: CompletionItem[] = [
  { label: "foobar", replacement: "foobar", range: range(0, 3) },
  { label: "foozle", replacement: "foozle", range: range(0, 3) },
];
const fooResponse: CompletionResponse = { version: 0, items: fooItems };

test("items map to the current version as the user keeps typing", () => {
  const { editor, session } = makeSession("foo", fooResponse);
  // user types "b", "a" at the cursor (end of the replacement range)
  editor.document.insert(3, "b");
  editor.document.insert(4, "a");
  assert.ok(session.isValid());
  assert.deepEqual(session.items()[0]!.range, { start: 0, end: 5 });
  assert.equal(editor.getText(), "fooba");
});

test("accepting a stale-response item replaces the grown range", () => {
  const { editor, session } = makeSession("foo", fooResponse);
  editor.document.insert(3, "b");
  editor.document.insert(4, "a");
  const tx = session.accept(fooItems[0]!);
  assert.equal(editor.getText(), "foobar");
  assert.equal(tx.edits.length, 1);
  assert.deepEqual(tx.edits[0], { start: 0, end: 5, text: "foobar" });
});

test("accepting is a single undoable transaction", () => {
  const { editor, session } = makeSession("foo", fooResponse);
  editor.document.insert(3, "b"); // "foob"
  session.accept(fooItems[0]!);
  assert.equal(editor.getText(), "foobar");
  editor.undo(); // one undo reverts the whole accept
  assert.equal(editor.getText(), "foob");
  assert.ok(!editor.canUndo); // the raw insert was not a transaction
});

test("an edit touching the replacement interior invalidates the response", () => {
  const { editor, session } = makeSession("foo", fooResponse);
  editor.document.delete(1, 2); // deletes interior char "o"
  assert.ok(!session.isValid());
  assert.throws(() => session.items(), StaleCompletionError);
  assert.throws(() => session.accept(fooItems[0]!), StaleCompletionError);
  assert.equal(editor.getText(), "fo"); // nothing was applied
});

test("an insertion strictly inside the range invalidates the response", () => {
  const { editor, session } = makeSession("foo", fooResponse);
  editor.document.insert(1, "X");
  assert.ok(!session.isValid());
});

test("edits outside the range keep the response valid and shift it", () => {
  const items: CompletionItem[] = [{ label: "barfly", replacement: "barfly", range: range(6, 9) }];
  const { editor, session } = makeSession("foo + bar", { version: 0, items });
  editor.document.insert(0, ">> ");
  assert.ok(session.isValid());
  assert.deepEqual(session.items()[0]!.range, { start: 9, end: 12 });
  session.accept(items[0]!);
  assert.equal(editor.getText(), ">> foo + barfly");
});

test("a response from a future or untracked version is rejected up front", () => {
  const editor = new TransactionalEditor("foo");
  assert.throws(
    () => new CompletionSession(editor, { version: 3, items: [] }),
    StaleCompletionError,
  );
});

test("multi-cursor accept maps every cursor range and applies one transaction", () => {
  const items: CompletionItem[] = [{ label: "foobar", replacement: "foobar", range: range(0, 2) }];
  const { editor, session } = makeSession("fo x fo", { version: 0, items });
  // multi-cursor typing: "o" at both cursors, one transaction
  editor.transact([edit(2, 2, "o"), edit(7, 7, "o")]);
  assert.equal(editor.getText(), "foo x foo");
  // primary range [0,2) and secondary cursor range [5,7), both anchored at v0
  const tx = session.accept(items[0]!, [range(5, 7)]);
  assert.equal(editor.getText(), "foobar x foobar");
  assert.equal(tx.edits.length, 2);
  // one undo reverts both replacements together
  editor.undo();
  assert.equal(editor.getText(), "foo x foo");
  // the typing transaction is separately undoable
  editor.undo();
  assert.equal(editor.getText(), "fo x fo");
});

test("multi-cursor accept fails atomically when any cursor range is stale", () => {
  const items: CompletionItem[] = [{ label: "foo", replacement: "foo", range: range(0, 2) }];
  const { editor, session } = makeSession("fo x fo", { version: 0, items });
  editor.document.delete(6, 7); // touches interior of secondary range [5,7)
  assert.throws(() => session.accept(items[0]!, [range(5, 7)]), StaleCompletionError);
  assert.equal(editor.getText(), "fo x f"); // primary replacement was not applied
});

test("overlapping mapped cursor ranges are rejected", () => {
  const items: CompletionItem[] = [{ label: "foo", replacement: "foo", range: range(0, 2) }];
  const { session } = makeSession("fo", { version: 0, items });
  assert.throws(() => session.accept(items[0]!, [range(1, 2)]), StaleCompletionError);
});

test("undo after accept keeps the chain append-only so old responses stay checkable", () => {
  const { editor, session } = makeSession("foo", fooResponse);
  session.accept(fooItems[0]!); // v1
  assert.equal(editor.getText(), "foobar");
  editor.undo(); // v2: inverse edit appended, versions never go backwards
  assert.equal(editor.getText(), "foo");
  assert.equal(session.responseVersion, 0);
  assert.equal(editor.version, 2);
  assert.ok(editor.document.chain.hasVersion(0));
});
