import { test } from "node:test";
import assert from "node:assert/strict";
import {
  NameEncodingError,
  MAX_SEGMENT_BYTES,
  encodeSegment,
  frame,
} from "../src/encoding.ts";

const hex = (bytes: Uint8Array): string =>
  [...bytes].map((b) => b.toString(16).padStart(2, "0")).join("");

const utf8 = (s: string): Uint8Array => new TextEncoder().encode(s);

test("名称段编码为 UTF-8 字节", () => {
  assert.equal(hex(encodeSegment("case-1")), "636173652d31");
  // 测 U+6D4B 试 U+8BD5
  assert.equal(hex(encodeSegment("测试")), "e6b58be8af95");
});

test("帧编码为长度前缀拼接，且是单射", () => {
  assert.equal(
    hex(frame([utf8("ab"), utf8("c")])),
    "0000000261620000000163",
  );
  // 长度前缀消除拼接歧义：["ab","c"] 与 ["a","bc"] 编码不同
  assert.notEqual(
    hex(frame([utf8("ab"), utf8("c")])),
    hex(frame([utf8("a"), utf8("bc")])),
  );
  // 空帧合法（用于根种子之外的场景），编码为空
  assert.equal(frame([]).length, 0);
});

test("拒绝空名称段", () => {
  assert.throws(() => encodeSegment(""), NameEncodingError);
});

test("拒绝包含路径分隔符的名称段", () => {
  assert.throws(() => encodeSegment("a/b"), NameEncodingError);
  assert.throws(() => encodeSegment("/"), NameEncodingError);
});

test("拒绝包含 NUL 的名称段", () => {
  assert.throws(() => encodeSegment("a" + String.fromCharCode(0) + "b"), NameEncodingError);
});

test("拒绝非 NFC 规范形式的名称段", () => {
  // e + 组合用尖音符（U+0301），与预组合的 é（U+00E9）显示相同
  const combining = "e" + String.fromCharCode(0x0301);
  const precomposed = String.fromCharCode(0x00e9);
  assert.throws(() => encodeSegment(combining), NameEncodingError);
  assert.doesNotThrow(() => encodeSegment(precomposed));
});

test("拒绝孤立代理项", () => {
  assert.throws(() => encodeSegment("a" + String.fromCharCode(0xd800)), NameEncodingError);
  assert.throws(() => encodeSegment(String.fromCharCode(0xdc00) + "b"), NameEncodingError);
  // 合法的代理对（如 emoji）不受影响
  assert.doesNotThrow(() => encodeSegment("用例" + String.fromCharCode(0xd83d, 0xde00)));
});

test("拒绝超长名称段", () => {
  assert.throws(() => encodeSegment("x".repeat(MAX_SEGMENT_BYTES + 1)), NameEncodingError);
  assert.doesNotThrow(() => encodeSegment("x".repeat(MAX_SEGMENT_BYTES)));
});

test("拒绝非字符串输入", () => {
  assert.throws(() => encodeSegment(42 as unknown as string), NameEncodingError);
  assert.throws(() => encodeSegment(undefined as unknown as string), NameEncodingError);
});
