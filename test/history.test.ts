import { test } from "node:test";
import assert from "node:assert/strict";
import { BranchingHistory, splitsSurrogatePair } from "../src/history.ts";

test("连续键入合并为一个节点，一次 undo 全部撤销", () => {
  const doc = new BranchingHistory();
  doc.insert("a");
  doc.insert("b");
  assert.equal(doc.text, "ab");
  assert.equal(doc.nodeCount(), 2); // root + 1 个合并节点
  doc.undo();
  assert.equal(doc.text, "");
});

test("光标移动打断键入合并", () => {
  const doc = new BranchingHistory();
  doc.insert("a");
  doc.setSelection(0); // 光标移动
  doc.insert("b");
  assert.equal(doc.text, "ba");
  assert.equal(doc.nodeCount(), 3); // root + 2 个节点，未合并
});

test("显式事务边界 commit() 打断键入合并", () => {
  const doc = new BranchingHistory();
  doc.insert("a");
  doc.commit();
  doc.insert("b");
  assert.equal(doc.text, "ab");
  assert.equal(doc.nodeCount(), 3);
  doc.undo(); // 只撤销 "b"
  assert.equal(doc.text, "a");
});

test("连续退格合并", () => {
  const doc = new BranchingHistory("hello");
  doc.setSelection(5);
  doc.deleteBackward();
  doc.deleteBackward();
  assert.equal(doc.text, "hel");
  assert.equal(doc.nodeCount(), 2);
  doc.undo();
  assert.equal(doc.text, "hello");
});

test("输入 ab 撤销再输入 x，保留 ab 与 x 两条分支", () => {
  const doc = new BranchingHistory();
  doc.insert("a");
  doc.insert("b");
  const abBranchId = doc.currentNodeId;
  doc.undo();
  doc.insert("x");

  const branches = doc.branches();
  assert.equal(branches.length, 2);
  const texts = branches.map((b) => b.text).sort();
  assert.deepEqual(texts, ["ab", "x"]);
  assert.ok(branches.some((b) => b.id === abBranchId));
});

test("切换分支时文本和选择区一起恢复", () => {
  const doc = new BranchingHistory();
  doc.insert("a");
  doc.insert("b");
  const abId = doc.currentNodeId;
  doc.undo();
  doc.insert("x");
  const xId = doc.currentNodeId;

  assert.equal(doc.text, "x");
  assert.deepEqual(doc.selectionRange, { anchor: 1, focus: 1 });

  assert.equal(doc.switchTo(abId), true);
  assert.equal(doc.text, "ab");
  assert.deepEqual(doc.selectionRange, { anchor: 2, focus: 2 });

  assert.equal(doc.switchTo(xId), true);
  assert.equal(doc.text, "x");
  assert.deepEqual(doc.selectionRange, { anchor: 1, focus: 1 });
});

test("undo/redo 沿最近分支往返", () => {
  const doc = new BranchingHistory();
  doc.insert("a");
  doc.commit();
  doc.insert("b");
  doc.undo();
  assert.equal(doc.text, "a");
  doc.redo();
  assert.equal(doc.text, "ab");
});

test("公开偏移为 UTF-16，编辑范围不切开代理对", () => {
  const doc = new BranchingHistory("a😀b"); // 😀 占 2 个 UTF-16 code unit
  assert.equal(doc.text.length, 4);
  assert.ok(splitsSurrogatePair(doc.text, 2));
  // 试图删除 [2,3)：恰好切开 😀，应外扩为删除整个 emoji
  doc.replace(2, 3, "");
  assert.equal(doc.text, "ab");
});

test("插入位置落在代理对中间时向外扩展", () => {
  const doc = new BranchingHistory("x😀y");
  doc.replace(2, 2, "Z"); // 在 😀 中间插入 → 扩展为替换整个 emoji
  assert.equal(doc.text, "xZy");
});

test("setSelection 不会把光标放进代理对中间", () => {
  const doc = new BranchingHistory("😀");
  doc.setSelection(1); // 1 在代理对中间 → 钳到 0
  assert.equal(doc.selectionRange.focus, 0);
});

test("退格一次删除完整 emoji（不留下半个代理对）", () => {
  const doc = new BranchingHistory("a😀");
  doc.setSelection(3);
  doc.deleteBackward();
  assert.equal(doc.text, "a");
});

test("LF 与 CRLF 文本都可按 UTF-16 偏移编辑", () => {
  const doc = new BranchingHistory("one\r\ntwo\nthree");
  // 在 CRLF 之后插入（"one\r\n" 长度为 5）
  doc.replace(5, 5, "TWO2\r\n");
  assert.equal(doc.text, "one\r\nTWO2\r\ntwo\nthree");
  // 删除 LF 行 "two\n"（偏移 11..15）
  doc.replace(11, 15, "");
  assert.equal(doc.text, "one\r\nTWO2\r\nthree");
  doc.undo();
  assert.equal(doc.text, "one\r\nTWO2\r\ntwo\nthree");
});

test("压缩后仍能回放每个可见节点，存活分支基线不被破坏", () => {
  const doc = new BranchingHistory();
  doc.insert("ab"); // 节点1（合并）
  doc.commit();
  doc.insert("cd"); // 节点2 → "abcd"
  doc.undo(); // 回到 "ab"
  doc.insert("xy"); // 节点3 → "abxy"（新分支）

  // 压缩前记录所有节点的回放文本
  const before = new Map(doc.nodes().map((n) => [n.id, doc.textAt(n.id)!]));
  assert.equal(before.size, 4); // root, ab, abcd, abxy

  const reclaimed = doc.compact();
  assert.ok(reclaimed > 0);

  // 每个仍可见的节点都能回放，且文本与压缩前一致
  const remaining = doc.nodes();
  assert.ok(remaining.length < before.size);
  for (const info of remaining) {
    const replayed = doc.textAt(info.id);
    assert.notEqual(replayed, undefined);
    if (before.has(info.id)) {
      assert.equal(replayed, before.get(info.id));
    }
  }

  // 两条存活分支的基线仍然完整
  const texts = doc.branches().map((b) => b.text).sort();
  assert.deepEqual(texts, ["abcd", "abxy"]);

  // 压缩后还能继续切换分支
  const abcd = doc.branches().find((b) => b.text === "abcd")!;
  assert.equal(doc.switchTo(abcd.id), true);
  assert.equal(doc.text, "abcd");
});

test("容量超限自动回收，不破坏存活分支基线", () => {
  const doc = new BranchingHistory("", { maxNodes: 3 });
  const expected: string[] = [];
  let text = "";
  for (const ch of ["a", "b", "c", "d", "e", "f"]) {
    doc.commit(); // 每个字符一个独立节点
    doc.insert(ch);
    text += ch;
    expected.push(text);
  }
  assert.equal(doc.text, "abcdef");
  assert.ok(doc.nodeCount() <= 3);
  // 当前（唯一存活）分支的基线文本完好
  assert.equal(doc.branches().length, 1);
  assert.equal(doc.branches()[0].text, "abcdef");
});

test("容量回收保留多分支：共享前缀被折叠，分支都可回放", () => {
  const doc = new BranchingHistory("", { maxNodes: 4 });
  doc.insert("ab");
  doc.commit();
  doc.insert("cd");
  doc.undo();
  doc.insert("xy");
  assert.ok(doc.nodeCount() <= 4);
  const texts = doc.branches().map((b) => b.text).sort();
  assert.deepEqual(texts, ["abcd", "abxy"]);
  for (const info of doc.nodes()) {
    assert.notEqual(doc.textAt(info.id), undefined);
  }
});
