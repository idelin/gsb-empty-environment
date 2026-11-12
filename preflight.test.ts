import assert from "node:assert/strict";
import test from "node:test";
import v8 from "node:v8";
import { preflightV8Payload } from "./preflight.ts";

const HEADER = [0xff, 0x0f];

const T = 0x54; // 'T'
const I = 0x49; // 'I'
const N = 0x4e; // 'N'
const STR = 0x22; // '"'
const OBJ = 0x6f; // 'o'
const OBJ_END = 0x7b; // '{'
const ARR = 0x41; // 'A'
const ARR_END = 0x24; // '$'
const REF = 0x5e; // '^'
const HOST_OBJECT = 0x5c; // '\'

function bytes(...parts: number[]): Uint8Array {
  return Uint8Array.from([...HEADER, ...parts]);
}

/** depth 层嵌套数组：A 01 A 01 ... T ... $ 00 01 $ 00 01 */
function nestedArrays(depth: number): Uint8Array {
  const out: number[] = [...HEADER];
  for (let i = 0; i < depth; i++) out.push(ARR, 0x01);
  out.push(T);
  for (let i = 0; i < depth; i++) out.push(ARR_END, 0x00, 0x01);
  return Uint8Array.from(out);
}

test("合法嵌套对象通过预检，且与 v8.deserialize 结果一致", () => {
  // { k: [1, 1.5] }
  const payload = bytes(
    OBJ,
    STR, 0x01, 0x6b, // "k"
    ARR, 0x02,
    I, 0x02, // zigzag(1)
    N, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf8, 0x3f, // 1.5
    ARR_END, 0x00, 0x02,
    OBJ_END, 0x01,
  );
  assert.deepEqual(preflightV8Payload(payload), { ok: true });
  assert.deepEqual(v8.deserialize(Buffer.from(payload)), { k: [1, 1.5] });
});

test("真实 v8.serialize 输出（含对象引用）通过预检", () => {
  const shared = { x: 1 };
  const value = {
    a: 1,
    b: [1, 2, "x", null, undefined, true, false, 3.14],
    c: [shared, shared], // 同一对象出现两次，产生 ^ 引用
  };
  const payload = v8.serialize(value);
  assert.ok(payload.includes(REF), "样本应包含对象引用标签");
  assert.deepEqual(preflightV8Payload(payload), { ok: true });
  const restored = v8.deserialize(payload);
  assert.deepEqual(restored, value);
  assert.equal(restored.c[0], restored.c[1]);
});

test("宿主对象标签 \\ 被拒绝", () => {
  const result = preflightV8Payload(bytes(HOST_OBJECT, 0x00));
  assert.equal(result.ok, false);
  assert.match(result.ok ? "" : result.reason, /0x5c/);
});

test("缓冲区、Map、Set、RegExp、BigInt 的真实序列化输出被拒绝", () => {
  const samples: unknown[] = [
    new ArrayBuffer(4),
    new Uint8Array([1, 2, 3]),
    new Map([["a", 1]]),
    new Set([1]),
    /abc/g,
    10n,
  ];
  for (const sample of samples) {
    const result = preflightV8Payload(v8.serialize(sample));
    assert.equal(result.ok, false, `${String(sample)} 应被拒绝`);
  }
});

test("未知标签被拒绝", () => {
  assert.equal(preflightV8Payload(bytes(0x00)).ok, false);
  assert.equal(preflightV8Payload(bytes(0x7a)).ok, false);
});

test("头部必须为 0xFF 加版本 15", () => {
  assert.equal(preflightV8Payload(new Uint8Array()).ok, false);
  assert.equal(preflightV8Payload(Uint8Array.from([0xff])).ok, false);
  assert.equal(preflightV8Payload(Uint8Array.from([0x00, 0x0f, T])).ok, false);
  assert.equal(preflightV8Payload(Uint8Array.from([0xff, 0x0e, T])).ok, false);
  assert.deepEqual(preflightV8Payload(Uint8Array.from([0xff, 0x0f, T])), {
    ok: true,
  });
});

test("32 层嵌套通过，33 层被拒绝", () => {
  assert.deepEqual(preflightV8Payload(nestedArrays(32)), { ok: true });
  const deep = preflightV8Payload(nestedArrays(33));
  assert.equal(deep.ok, false);
  assert.match(deep.ok ? "" : deep.reason, /嵌套/);
});

test("前向引用被拒绝，合法回引用通过", () => {
  // 顶层直接引用 0 号对象：尚无任何对象出现
  assert.equal(preflightV8Payload(bytes(REF, 0x00)).ok, false);
  // 对象内引用未出现的 5 号对象
  const forward = bytes(OBJ, STR, 0x01, 0x6b, REF, 0x05, OBJ_END, 0x01);
  const forwardResult = preflightV8Payload(forward);
  assert.equal(forwardResult.ok, false);
  assert.match(forwardResult.ok ? "" : forwardResult.reason, /引用编号 5/);
  // 数组 [s, ^1]：引用已出现的字符串
  const backRef = bytes(
    ARR, 0x02,
    STR, 0x01, 0x78, // "x"（编号 1）
    REF, 0x01,
    ARR_END, 0x00, 0x02,
  );
  assert.deepEqual(preflightV8Payload(backRef), { ok: true });
});

test("截断字符串被拒绝", () => {
  // 声明长度 10，实际只有 2 字节
  const truncated = bytes(STR, 0x0a, 0x61, 0x62);
  const result = preflightV8Payload(truncated);
  assert.equal(result.ok, false);
  assert.match(result.ok ? "" : result.reason, /字符串长度超出剩余字节/);
});

test("属性与元素总数超过 4096 被拒绝", () => {
  // 长度 4097 的稠密数组，元素全为 true
  const length = 4097;
  const out: number[] = [...HEADER, ARR, 0x81, 0x20]; // varint(4097)
  for (let i = 0; i < length; i++) out.push(T);
  out.push(ARR_END, 0x00, 0x81, 0x20);
  const result = preflightV8Payload(Uint8Array.from(out));
  assert.equal(result.ok, false);
  assert.match(result.ok ? "" : result.reason, /4096/);
});

test("顶层值之后的多余字节被拒绝", () => {
  assert.equal(preflightV8Payload(bytes(T, T)).ok, false);
});

test("声明属性数与实际不符被拒绝", () => {
  // 对象声明 2 个属性，实际只有 1 个
  const payload = bytes(OBJ, STR, 0x01, 0x6b, I, 0x02, OBJ_END, 0x02);
  assert.equal(preflightV8Payload(payload).ok, false);
});
