import assert from "node:assert/strict";
import test from "node:test";
import v8 from "node:v8";

import {
  MAX_DEPTH,
  MAX_PROPERTIES,
  PreflightError,
  assertV8Payload,
  validateV8Payload,
} from "../index.ts";

const HEADER = [0xff, 0x0f];

function bytes(...parts: Array<number | number[]>): Uint8Array {
  return Uint8Array.from(parts.flat());
}

function encodeVarint(value: number): number[] {
  const out: number[] = [];
  let rest = value;
  do {
    let byte = rest % 128;
    rest = Math.floor(rest / 128);
    if (rest > 0) byte |= 0x80;
    out.push(byte);
  } while (rest > 0);
  return out;
}

/** 构造 depth 层嵌套数组 [[...[null]...]]。 */
function nestedArrays(depth: number): Uint8Array {
  const out = [...HEADER];
  for (let i = 0; i < depth; i += 1) out.push(0x41, 0x01); // A, length 1
  out.push(0x30); // null
  for (let i = 0; i < depth; i += 1) out.push(0x24, 0x00, 0x01); // $, 0 props, length 1
  return Uint8Array.from(out);
}

/** 构造拥有 count 个属性的对象 {k0: null, k1: null, ...}。 */
function wideObject(count: number): Uint8Array {
  const out = [...HEADER, 0x6f]; // o
  for (let i = 0; i < count; i += 1) {
    const key = `k${i}`;
    out.push(0x22, key.length, ...[...key].map((c) => c.charCodeAt(0)));
    out.push(0x30); // null
  }
  out.push(0x7b, ...encodeVarint(count)); // {, property count
  return Uint8Array.from(out);
}

function expectRejected(data: Uint8Array, code: string): void {
  const result = validateV8Payload(data);
  assert.equal(result.ok, false, `expected rejection with ${code}`);
  assert.equal((result as { code: string }).code, code);
  assert.throws(() => assertV8Payload(data), (error: unknown) => {
    assert.ok(error instanceof PreflightError);
    assert.equal(error.code, code);
    return true;
  });
}

test("合法嵌套对象 {a: [1, 2], b: true} 放行", () => {
  // 与 v8.serialize({a: [1, 2], b: true}) 逐字节一致。
  const payload = bytes(
    HEADER,
    0x6f, // o
    [0x22, 0x01, 0x61], // "a"
    [0x41, 0x02], // A, length 2
    [0x49, 0x02], // I 1 (zigzag)
    [0x49, 0x04], // I 2 (zigzag)
    [0x24, 0x00, 0x02], // $, 0 props, length 2
    [0x22, 0x01, 0x62], // "b"
    0x54, // T
    [0x7b, 0x02], // {, 2 props
  );
  assert.deepEqual(validateV8Payload(payload), { ok: true });
  assert.doesNotThrow(() => assertV8Payload(payload));
});

test("v8.serialize 产出的合法载荷放行且可往返", () => {
  const samples: unknown[] = [
    undefined,
    null,
    true,
    42,
    -7,
    3.14,
    "hello",
    [1, "two", [3]],
    { a: [1, 2], b: "x", c: { d: null } },
  ];
  const cyclic: unknown[] = [1];
  cyclic.push(cyclic);
  samples.push(cyclic);
  for (const sample of samples) {
    const payload = v8.serialize(sample);
    assert.deepEqual(validateV8Payload(payload), { ok: true });
    assert.deepEqual(v8.deserialize(payload), sample);
  }
});

test("宿主对象标签 \\ 拒绝", () => {
  expectRejected(bytes(HEADER, 0x5c), "FORBIDDEN_TAG");
});

test("缓冲区 / Map / Set / RegExp / BigInt 标签拒绝", () => {
  expectRejected(bytes(HEADER, 0x42), "FORBIDDEN_TAG"); // B ArrayBuffer
  expectRejected(bytes(HEADER, 0x56), "FORBIDDEN_TAG"); // V ArrayBufferView
  expectRejected(bytes(HEADER, 0x3b), "FORBIDDEN_TAG"); // ; Map
  expectRejected(bytes(HEADER, 0x27), "FORBIDDEN_TAG"); // ' Set
  expectRejected(bytes(HEADER, 0x52), "FORBIDDEN_TAG"); // R RegExp
  expectRejected(bytes(HEADER, 0x5a), "FORBIDDEN_TAG"); // Z BigInt
  expectRejected(v8.serialize(1n), "FORBIDDEN_TAG");
  expectRejected(v8.serialize(new Map()), "FORBIDDEN_TAG");
  expectRejected(v8.serialize(new Uint8Array(1)), "FORBIDDEN_TAG");
});

test("未知标签拒绝", () => {
  expectRejected(bytes(HEADER, 0x01), "UNKNOWN_TAG");
});

test("33 层嵌套拒绝，32 层放行", () => {
  assert.deepEqual(validateV8Payload(nestedArrays(MAX_DEPTH)), { ok: true });
  expectRejected(nestedArrays(MAX_DEPTH + 1), "DEPTH_EXCEEDED");
});

test("前向引用拒绝，回指引用放行", () => {
  // 顶层直接引用尚不存在的 0 号对象。
  expectRejected(bytes(HEADER, 0x5e, 0x00), "BAD_REFERENCE");
  // 数组第二个元素引用尚未出现的 1 号对象。
  expectRejected(
    bytes(HEADER, [0x41, 0x02], 0x30, [0x5e, 0x01], [0x24, 0x00, 0x02]),
    "BAD_REFERENCE",
  );
  // 数组引用自身（编号 0 在 A 开始时即登记），与 v8.serialize 的循环数组一致。
  const selfRef = bytes(HEADER, [0x41, 0x01], [0x5e, 0x00], [0x24, 0x00, 0x01]);
  assert.deepEqual(validateV8Payload(selfRef), { ok: true });
});

test("截断字符串拒绝", () => {
  // 声明 5 字节，实际只剩 2 字节。
  expectRejected(
    bytes(HEADER, [0x22, 0x05, 0x61, 0x62]),
    "STRING_OUT_OF_BOUNDS",
  );
  // 声明长度后一个字节都没有。
  expectRejected(bytes(HEADER, [0x22, 0x01]), "STRING_OUT_OF_BOUNDS");
});

test("头部与版本校验", () => {
  expectRejected(bytes(), "EMPTY_PAYLOAD");
  expectRejected(bytes(0x00, 0x0f, 0x30), "BAD_HEADER");
  expectRejected(bytes(0xff), "UNEXPECTED_EOF");
  expectRejected(bytes(0xff, 0x0e, 0x30), "UNSUPPORTED_VERSION");
});

test("属性与元素总数上限 4096", () => {
  assert.deepEqual(validateV8Payload(wideObject(MAX_PROPERTIES)), { ok: true });
  expectRejected(wideObject(MAX_PROPERTIES + 1), "TOO_MANY_PROPERTIES");
});

test("声明数量与实际不符拒绝", () => {
  // 对象实际 1 个属性，{ 后声明 2 个。
  const object = bytes(
    HEADER,
    0x6f,
    [0x22, 0x01, 0x61],
    0x30,
    [0x7b, 0x02],
  );
  expectRejected(object, "COUNT_MISMATCH");
  // 数组 $ 后声明的长度与头部不一致。
  const array = bytes(HEADER, [0x41, 0x01], 0x30, [0x24, 0x00, 0x02]);
  expectRejected(array, "COUNT_MISMATCH");
});

test("截断与尾随字节拒绝", () => {
  expectRejected(bytes(HEADER, 0x4e, 0x00, 0x00), "UNEXPECTED_EOF"); // double 不足 8 字节
  expectRejected(bytes(HEADER, 0x49), "UNEXPECTED_EOF"); // int32 缺 varint
  expectRejected(bytes(HEADER, 0x30, 0x30), "TRAILING_BYTES"); // 顶层值后多一字节
  expectRejected(bytes(HEADER, [0x49, 0x80, 0x80, 0x80, 0x80, 0x80]), "MALFORMED_VARINT");
});
