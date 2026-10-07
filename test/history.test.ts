import { test } from "node:test";
import assert from "node:assert/strict";
import { History } from "../src/history.ts";
import { DrawingEditor } from "../src/editor.ts";
import type { DrawingDocument } from "../src/editor.ts";

function docOf(...objects: Array<[string, number, number]>): DrawingDocument {
  const objectsRecord: DrawingDocument["objects"] = {};
  for (const [id, x, y] of objects) {
    objectsRecord[id] = { id, x, y };
  }
  return { objects: objectsRecord };
}

function assertSnapshotSequence(
  actual: DrawingDocument[],
  expected: DrawingDocument[],
): void {
  assert.equal(
    actual.length,
    expected.length,
    `snapshot sequence length mismatch: ${JSON.stringify(actual)}`,
  );
  for (let i = 0; i < expected.length; i++) {
    assert.deepEqual(actual[i], expected[i], `snapshot #${i} mismatch`);
  }
}

test("连续同对象位移合并为一次撤销", () => {
  const editor = new DrawingEditor(docOf(["a", 0, 0]));
  const seen: DrawingDocument[] = [editor.snapshot];

  editor.moveObject("a", 1, 0);
  seen.push(editor.snapshot);
  editor.moveObject("a", 2, 3);
  seen.push(editor.snapshot);
  editor.moveObject("a", -1, 1);
  seen.push(editor.snapshot);

  assert.equal(editor.history.undoDepth, 1);

  editor.undo();
  seen.push(editor.snapshot);
  assert.equal(editor.history.canUndo(), false);

  editor.redo();
  seen.push(editor.snapshot);

  assertSnapshotSequence(seen, [
    docOf(["a", 0, 0]),
    docOf(["a", 1, 0]),
    docOf(["a", 3, 3]),
    docOf(["a", 2, 4]),
    docOf(["a", 0, 0]),
    docOf(["a", 2, 4]),
  ]);
});

test("中间穿插其他对象的操作则不合并", () => {
  const editor = new DrawingEditor(docOf(["a", 0, 0], ["b", 10, 10]));

  editor.moveObject("a", 1, 0);
  editor.moveObject("b", 0, 5);
  editor.moveObject("a", 1, 0);

  assert.equal(editor.history.undoDepth, 3);

  const seen: DrawingDocument[] = [];
  while (editor.undo()) {
    seen.push(editor.snapshot);
  }

  assertSnapshotSequence(seen, [
    docOf(["a", 1, 0], ["b", 10, 15]),
    docOf(["a", 1, 0], ["b", 10, 10]),
    docOf(["a", 0, 0], ["b", 10, 10]),
  ]);
});

test("事务标签不同则不合并", () => {
  const history = new History<DrawingDocument>(docOf(["a", 0, 0]));

  history.commit({ label: "move", mergeKey: "a", doc: docOf(["a", 1, 0]) });
  history.commit({ label: "nudge", mergeKey: "a", doc: docOf(["a", 2, 0]) });
  history.commit({ label: "move", mergeKey: "a", doc: docOf(["a", 3, 0]) });

  assert.equal(history.undoDepth, 3);
  assert.deepEqual(history.undo(), docOf(["a", 2, 0]));
  assert.deepEqual(history.undo(), docOf(["a", 1, 0]));
  assert.deepEqual(history.undo(), docOf(["a", 0, 0]));
});

test("撤销后分叉：新提交清空重做栈且不与撤销前事务合并", () => {
  const editor = new DrawingEditor(docOf(["a", 0, 0]));
  const seen: DrawingDocument[] = [editor.snapshot];

  editor.moveObject("a", 1, 0);
  seen.push(editor.snapshot);
  editor.moveObject("a", 1, 0);
  seen.push(editor.snapshot);

  editor.undo();
  seen.push(editor.snapshot);
  assert.equal(editor.history.canRedo(), true);

  editor.moveObject("a", 0, 9);
  seen.push(editor.snapshot);

  assert.equal(editor.history.canRedo(), false);
  assert.equal(editor.history.undoDepth, 1);

  editor.undo();
  seen.push(editor.snapshot);
  assert.equal(editor.history.canUndo(), false);

  assertSnapshotSequence(seen, [
    docOf(["a", 0, 0]),
    docOf(["a", 1, 0]),
    docOf(["a", 2, 0]),
    docOf(["a", 0, 0]),
    docOf(["a", 0, 9]),
    docOf(["a", 0, 0]),
  ]);
});

test("容量淘汰：超出容量的最旧事务被丢弃", () => {
  const editor = new DrawingEditor(docOf(["a", 0, 0]), 3);

  editor.addObject({ id: "b", x: 1, y: 1 });
  editor.addObject({ id: "c", x: 2, y: 2 });
  editor.addObject({ id: "d", x: 3, y: 3 });
  editor.addObject({ id: "e", x: 4, y: 4 });
  editor.moveObject("a", 5, 5);

  assert.equal(editor.history.undoDepth, 3);

  const seen: DrawingDocument[] = [];
  while (editor.undo()) {
    seen.push(editor.snapshot);
  }

  assertSnapshotSequence(seen, [
    docOf(["a", 0, 0], ["b", 1, 1], ["c", 2, 2], ["d", 3, 3], ["e", 4, 4]),
    docOf(["a", 0, 0], ["b", 1, 1], ["c", 2, 2], ["d", 3, 3]),
    docOf(["a", 0, 0], ["b", 1, 1], ["c", 2, 2]),
  ]);
});

test("保存点脏标记：编辑变脏，撤销回到保存内容则干净", () => {
  const editor = new DrawingEditor(docOf(["a", 0, 0], ["b", 10, 10]));
  editor.moveObject("a", 4, 4);
  editor.markSaved();
  assert.equal(editor.isDirty(), false);

  const seen: DrawingDocument[] = [];
  const dirtyFlags: boolean[] = [];

  editor.moveObject("b", 1, 1);
  seen.push(editor.snapshot);
  dirtyFlags.push(editor.isDirty());

  editor.undo();
  seen.push(editor.snapshot);
  dirtyFlags.push(editor.isDirty());

  editor.redo();
  seen.push(editor.snapshot);
  dirtyFlags.push(editor.isDirty());

  assertSnapshotSequence(seen, [
    docOf(["a", 4, 4], ["b", 11, 11]),
    docOf(["a", 4, 4], ["b", 10, 10]),
    docOf(["a", 4, 4], ["b", 11, 11]),
  ]);
  assert.deepEqual(dirtyFlags, [true, false, true]);
});

test("合并跨保存点仍按内容识别是否回到保存状态", () => {
  const editor = new DrawingEditor(docOf(["a", 0, 0]));

  editor.moveObject("a", 1, 0);
  editor.markSaved();

  editor.moveObject("a", 1, 0);
  assert.equal(editor.isDirty(), true);
  assert.equal(editor.history.undoDepth, 1, "跨保存点的连续位移仍合并为一条事务");

  editor.moveObject("a", -1, 0);
  assert.equal(editor.isDirty(), false, "合并后内容回到保存点则干净");

  const seen: DrawingDocument[] = [editor.snapshot];
  editor.undo();
  seen.push(editor.snapshot);
  assert.equal(editor.isDirty(), true, "撤销跨过保存点后内容不同则脏");

  editor.redo();
  seen.push(editor.snapshot);
  assert.equal(editor.isDirty(), false);

  assertSnapshotSequence(seen, [
    docOf(["a", 1, 0]),
    docOf(["a", 0, 0]),
    docOf(["a", 1, 0]),
  ]);
});

test("容量淘汰掉保存点位置后脏标记仍按内容判定", () => {
  const editor = new DrawingEditor(docOf(["a", 0, 0]), 2);
  editor.markSaved();

  editor.addObject({ id: "b", x: 1, y: 1 });
  editor.addObject({ id: "c", x: 2, y: 2 });
  editor.addObject({ id: "d", x: 3, y: 3 });

  assert.equal(editor.history.undoDepth, 2);
  assert.equal(editor.isDirty(), true, "保存点已被淘汰，无法撤销回去，仍为脏");

  editor.removeObject("d");
  editor.removeObject("c");
  editor.removeObject("b");
  assert.equal(editor.isDirty(), false, "内容回到保存快照则干净");
});

test("完整文档快照序列：混合操作端到端验证", () => {
  const editor = new DrawingEditor(undefined, 4);
  const seen: DrawingDocument[] = [editor.snapshot];
  const dirtyFlags: boolean[] = [editor.isDirty()];
  const record = (): void => {
    seen.push(editor.snapshot);
    dirtyFlags.push(editor.isDirty());
  };

  editor.addObject({ id: "a", x: 0, y: 0 });
  record();
  editor.addObject({ id: "b", x: 10, y: 10 });
  record();
  editor.markSaved();

  editor.moveObject("a", 1, 0);
  record();
  editor.moveObject("a", 1, 0);
  record();
  editor.moveObject("b", 0, 1);
  record();

  editor.undo();
  record();
  editor.undo();
  record();

  editor.moveObject("a", 0, 7);
  record();

  editor.undo();
  record();
  editor.undo();
  record();

  assertSnapshotSequence(seen, [
    docOf(),
    docOf(["a", 0, 0]),
    docOf(["a", 0, 0], ["b", 10, 10]),
    docOf(["a", 1, 0], ["b", 10, 10]),
    docOf(["a", 2, 0], ["b", 10, 10]),
    docOf(["a", 2, 0], ["b", 10, 11]),
    docOf(["a", 2, 0], ["b", 10, 10]),
    docOf(["a", 0, 0], ["b", 10, 10]),
    docOf(["a", 0, 7], ["b", 10, 10]),
    docOf(["a", 0, 0], ["b", 10, 10]),
    docOf(["a", 0, 0]),
  ]);
  assert.deepEqual(dirtyFlags, [
    false,
    true,
    true,
    true,
    true,
    true,
    true,
    false,
    true,
    false,
    true,
  ]);
});
