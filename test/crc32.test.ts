import { test } from "node:test";
import assert from "node:assert/strict";
import { crc32, crc32Hex } from "../src/crc32.ts";

test("CRC-32 标准校验向量", () => {
  // 标准测试向量：CRC-32("123456789") = 0xCBF43926
  assert.equal(crc32("123456789"), 0xcbf43926);
  assert.equal(crc32(""), 0x00000000);
  assert.equal(crc32Hex("123456789"), "cbf43926");
});

test("摘要恒为 4 字节无符号整数", () => {
  for (const s of ["node-1", "成员A:ALIVE:7", "x".repeat(1024)]) {
    const d = crc32(s);
    assert.ok(Number.isInteger(d) && d >= 0 && d <= 0xffffffff);
  }
  assert.equal(crc32Hex("任意输入").length, 8);
});
