import { test } from "node:test";
import assert from "node:assert/strict";
import { RandomStream } from "../src/stream.ts";

const hex = (bytes: Uint8Array): string =>
  [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");

test("分批生成与一次生成字节序列等价", () => {
  const oneShot = RandomStream.fromSeed("pairwise-gsb").child("suite").child("case-1");
  const batched = RandomStream.fromSeed("pairwise-gsb").child("suite").child("case-1");

  const expected = hex(oneShot.nextBytes(1000));

  // 以不规则的批次大小（刻意跨越 32 字节块边界）取同样多的字节
  const chunks: string[] = [];
  for (const size of [1, 31, 32, 33, 63, 64, 65, 127, 128, 129, 327]) {
    chunks.push(hex(batched.nextBytes(size)));
  }
  assert.equal(1 + 31 + 32 + 33 + 63 + 64 + 65 + 127 + 128 + 129 + 327, 1000);
  assert.equal(chunks.join(""), expected);
});

test("混合类型抽取与字节抽取共享同一游标", () => {
  const typed = RandomStream.fromSeed("pairwise-gsb").child("mixed");
  const raw = RandomStream.fromSeed("pairwise-gsb").child("mixed");

  // typed 流按类型抽取；raw 流取等价字节后手工重组，二者必须一致
  const u32 = typed.nextUint32();
  const u32Bytes = raw.nextBytes(4);
  const manualU32 =
    ((u32Bytes[0] << 24) | (u32Bytes[1] << 16) | (u32Bytes[2] << 8) | u32Bytes[3]) >>> 0;
  assert.equal(u32, manualU32);

  const f = typed.nextFloat();
  const fBytes = raw.nextBytes(7);
  const high = ((fBytes[0] << 24) | (fBytes[1] << 16) | (fBytes[2] << 8) | fBytes[3]) >>> 0;
  const low = (((fBytes[4] << 16) | (fBytes[5] << 8) | fBytes[6]) >>> 3);
  assert.equal(f, (high * 2097152 + low) / 9007199254740992);

  // 交错之后再取字节，两条流仍然对齐
  typed.nextInt(0, 100);
  raw.nextBytes(8);
  assert.equal(hex(typed.nextBytes(50)), hex(raw.nextBytes(50)));
});

test("nextInt 落在区间内且同路径重放一致", () => {
  const s1 = RandomStream.fromSeed("pairwise-gsb").child("dice");
  const s2 = RandomStream.fromSeed("pairwise-gsb").child("dice");
  for (let i = 0; i < 200; i++) {
    const v1 = s1.nextInt(1, 7);
    const v2 = s2.nextInt(1, 7);
    assert.equal(v1, v2);
    assert.ok(v1 >= 1 && v1 < 7);
  }
});

test("nextFloat 落在 [0, 1) 且同路径重放一致", () => {
  const s1 = RandomStream.fromSeed("pairwise-gsb").child("floats");
  const s2 = RandomStream.fromSeed("pairwise-gsb").child("floats");
  for (let i = 0; i < 200; i++) {
    const v1 = s1.nextFloat();
    const v2 = s2.nextFloat();
    assert.equal(v1, v2);
    assert.ok(v1 >= 0 && v1 < 1);
  }
});

test("参数校验", () => {
  const s = RandomStream.fromSeed("pairwise-gsb");
  assert.throws(() => s.nextBytes(-1), RangeError);
  assert.throws(() => s.nextBytes(1.5), RangeError);
  assert.throws(() => s.nextInt(5, 5), RangeError);
  assert.throws(() => s.nextInt(7, 3), RangeError);
  assert.throws(() => s.nextInt(0.5, 3), RangeError);
});
