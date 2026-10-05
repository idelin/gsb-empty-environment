import { test } from "node:test";
import assert from "node:assert/strict";
import { RandomStream } from "../src/stream.ts";

const hex = (bytes: Uint8Array): string =>
  [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");

// 跨平台固定向量：算法完全由整数运算与纯 TS SHA-256 定义，
// 任何平台、任何时刻运行都必须得到下列逐位相同的结果。
// 若未来修改了派生或输出算法，这些向量必须整体重新生成并记录变更原因。

test("固定向量：根流（种子 pairwise-gsb）", () => {
  const root = RandomStream.fromSeed("pairwise-gsb");
  assert.equal(
    root.keyHex,
    "3e157f54307e3ac4c551d1bc612b780ca21185dc2394f68efb4e615f1ae588be",
  );
  assert.equal(
    hex(root.nextBytes(64)),
    "f9c6cea6324da0ca224d6b7cbce1d0d53f0794b1315b4e707b299b2814bc2c53" +
      "325836ef1f4640d6e32fe0a872e0052a12d95a5d984fcb9ea749c3a703234486",
  );
});

test("固定向量：二级子流 suite/case-1", () => {
  const leaf = RandomStream.fromSeed("pairwise-gsb").child("suite").child("case-1");
  assert.equal(
    leaf.keyHex,
    "3b8b8a1152482e9d701672267faf4a4cf168a30aa03fb8eab7b7dfa0c487e711",
  );
  assert.equal(
    hex(leaf.nextBytes(32)),
    "b5666bd549967f2992d37de53c54304b7e654389080ae9849b79bb0eaf810d4f",
  );
  assert.deepEqual(
    [leaf.nextUint32(), leaf.nextUint32(), leaf.nextUint32(), leaf.nextUint32()],
    [379566023, 1133418121, 4013019587, 1709703718],
  );
  assert.deepEqual(
    [leaf.nextFloat(), leaf.nextFloat(), leaf.nextFloat()],
    [0.9176198173102271, 0.6014665861390861, 0.02620521039639767],
  );
  assert.deepEqual(
    [
      leaf.nextInt(1, 7),
      leaf.nextInt(1, 7),
      leaf.nextInt(1, 7),
      leaf.nextInt(1, 7),
      leaf.nextInt(1, 7),
      leaf.nextInt(0, 100),
    ],
    [6, 3, 3, 2, 6, 65],
  );
});

test("固定向量：非 ASCII 名称路径 套件/用例-一", () => {
  const leaf = RandomStream.fromSeed("pairwise-gsb").child("套件").child("用例-一");
  assert.equal(
    leaf.keyHex,
    "72e813ac9b8144594d41551889453bd1a09a3e87cc280b000babe6ef015f3691",
  );
  assert.equal(hex(leaf.nextBytes(16)), "ac51f822b0cdec3c5794bae33e46971d");
});

test("固定向量：Uint8Array 种子", () => {
  const seed = new Uint8Array([0xde, 0xad, 0xbe, 0xef]);
  const root = RandomStream.fromSeed(seed);
  const again = RandomStream.fromSeed(new Uint8Array([0xde, 0xad, 0xbe, 0xef]));
  assert.equal(root.keyHex, again.keyHex);
  assert.equal(hex(root.nextBytes(32)), hex(again.nextBytes(32)));
});
