import { test } from "node:test";
import assert from "node:assert/strict";
import { RandomStream } from "../src/stream.ts";
import { NameEncodingError } from "../src/encoding.ts";

const hex = (bytes: Uint8Array): string =>
  [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");

test("新增兄弟测试不改变已有兄弟的数据", () => {
  const root = RandomStream.fromSeed("pairwise-gsb");
  const before = hex(root.child("case-a").nextBytes(64));

  // 之后新增一批兄弟测试
  root.child("case-b").nextBytes(128);
  root.child("case-c").nextFloat();
  root.child("case-d").nextInt(0, 1000);

  const after = hex(root.child("case-a").nextBytes(64));
  assert.equal(after, before);
});

test("派生子流不消耗父流状态（child 是纯函数）", () => {
  const parent = RandomStream.fromSeed("pairwise-gsb");
  const first = parent.nextBytes(16);
  parent.child("whatever");
  parent.child("another");
  const second = parent.nextBytes(16);

  const fresh = RandomStream.fromSeed("pairwise-gsb");
  assert.deepEqual([...first], [...fresh.nextBytes(16)]);
  assert.deepEqual([...second], [...fresh.nextBytes(16)]);
});

test("重命名测试即得到全新的流", () => {
  const root = RandomStream.fromSeed("pairwise-gsb");
  const oldStream = root.child("case-old");
  const newStream = root.child("case-new");
  assert.notEqual(oldStream.keyHex, newStream.keyHex);
  assert.notEqual(hex(oldStream.nextBytes(32)), hex(newStream.nextBytes(32)));
});

test("兄弟流相互独立", () => {
  const root = RandomStream.fromSeed("pairwise-gsb");
  const keys = new Set<string>();
  for (let i = 0; i < 100; i++) {
    keys.add(root.child(`case-${i}`).keyHex);
  }
  assert.equal(keys.size, 100);
});

test("层级路径与拼接名称是不同的流", () => {
  const root = RandomStream.fromSeed("pairwise-gsb");
  const nested = root.child("suite").child("case");
  assert.notEqual(nested.keyHex, root.child("suite.case").keyHex);
  assert.notEqual(nested.keyHex, root.child("suitecase").keyHex);
  assert.notEqual(
    root.child("ab").child("c").keyHex,
    root.child("a").child("bc").keyHex,
  );
});

test("相同路径独立推导得到完全相同的流", () => {
  const p1 = RandomStream.fromSeed("pairwise-gsb").child("套件").child("用例-一");
  const p2 = RandomStream.fromSeed("pairwise-gsb").child("套件").child("用例-一");
  assert.equal(p1.keyHex, p2.keyHex);
  assert.equal(hex(p1.nextBytes(64)), hex(p2.nextBytes(64)));
});

test("不同种子得到不同的流", () => {
  const a = RandomStream.fromSeed("seed-a");
  const b = RandomStream.fromSeed("seed-b");
  assert.notEqual(a.keyHex, b.keyHex);
  assert.notEqual(hex(a.nextBytes(32)), hex(b.nextBytes(32)));
});

test("path 记录从根到当前流的名称路径", () => {
  const root = RandomStream.fromSeed("pairwise-gsb");
  assert.deepEqual([...root.path], []);
  const leaf = root.child("suite").child("case-1");
  assert.deepEqual([...leaf.path], ["suite", "case-1"]);
});

test("非法名称在派生时被拒绝", () => {
  const root = RandomStream.fromSeed("pairwise-gsb");
  assert.throws(() => root.child(""), NameEncodingError);
  assert.throws(() => root.child("a/b"), NameEncodingError);
  assert.throws(
    () => root.child("e" + String.fromCharCode(0x0301)),
    NameEncodingError,
  );
});

test("拒绝空种子", () => {
  assert.throws(() => RandomStream.fromSeed(""), RangeError);
  assert.throws(() => RandomStream.fromSeed(new Uint8Array(0)), RangeError);
});
