import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  encode, decode,
  ProtoCodecError, TruncatedError, OverflowError, InvalidWireTypeError,
  WireReader, WireWriter, WireType,
} from '../src/index.ts';
import type { Schema } from '../src/index.ts';

const Inner: Schema = [
  { no: 1, name: 'v', type: 'sint64' },
  { no: 2, name: 'label', type: 'string' },
];
const Outer: Schema = [
  { no: 1, name: 'inner', type: 'message', schema: Inner },
  { no: 2, name: 'nums', type: 'sint32', repeated: true, packed: true },
  { no: 3, name: 'payload', type: 'bytes' },
];
const value = {
  inner: { v: -0x8000_0000_0000_0000n, label: '边界' },
  nums: [-2147483648, -1, 0, 1, 2147483647],
  payload: new Uint8Array([1, 2, 3, 4, 5]),
};

test('every truncation point is safely guarded (no crash, no garbage)', () => {
  const full = encode(Outer, value);
  for (let cut = 0; cut < full.length; cut++) {
    const truncated = full.subarray(0, cut);
    try {
      decode(Outer, truncated); // 仅允许：恰好是合法短消息
    } catch (e) {
      assert.ok(e instanceof TruncatedError,
        `cut=${cut}: expected TruncatedError, got ${(e as Error).name}: ${(e as Error).message}`);
    }
  }
});

test('truncation inside nested message body is detected', () => {
  const full = encode(Outer, value);
  // 嵌套 message 的长度前缀声明了完整长度，截断其 body 必须报错
  const innerLen = encode(Inner, value.inner).length;
  const cutInsideInner = 2 + innerLen - 1; // tag(1)+len(1) 之后少 1 字节
  assert.throws(() => decode(Outer, full.subarray(0, cutInsideInner)), TruncatedError);
});

test('hostile length prefix cannot cause huge allocation or OOB read', () => {
  // field 3 (bytes) 声明 0xffffffff 字节，但缓冲区几乎没有数据
  const evil = new WireWriter().tag(3, WireType.LengthDelimited).varint(0xffffffff).byte(0x41).finish();
  assert.throws(() => decode(Outer, evil), TruncatedError);
});

test('truncated packed repeated body is detected', () => {
  const full = encode(Outer, value);
  const innerPart = 2 + encode(Inner, value.inner).length;
  // packed 字段: tag + len + body；切在 body 中间
  const packedBodyStart = innerPart + 2;
  assert.throws(() => decode(Outer, full.subarray(0, packedBodyStart + 1)), TruncatedError);
});

test('unsupported wire types (groups) are rejected', () => {
  const g = new WireWriter().tag(1, 3).finish(); // start-group
  assert.throws(() => decode(Outer, g), InvalidWireTypeError);
  const bad = new WireWriter().tag(1, 7).finish();
  assert.throws(() => decode(Outer, bad), InvalidWireTypeError);
});

test('field number 0 is rejected', () => {
  assert.throws(() => decode(Outer, new Uint8Array([0x00])), OverflowError);
  assert.throws(() => new WireWriter().tag(0, WireType.Varint), OverflowError);
});

test('varint overflow inside a message field is reported', () => {
  const evil = new Uint8Array([0x08, ...new Array(11).fill(0x80)]); // field1 varint 超长
  assert.throws(() => decode(Outer, evil), OverflowError);
});

test('all codec errors share a common base class and carry offsets', () => {
  const full = encode(Outer, value);
  try {
    decode(Outer, full.subarray(0, full.length - 1));
    assert.unreachable();
  } catch (e) {
    assert.ok(e instanceof ProtoCodecError);
    assert.equal(typeof (e as InstanceType<typeof ProtoCodecError>).offset, 'number');
  }
});

test('reader.skip bounds-checks every wire type', () => {
  for (const [wire, avail] of [[WireType.Fixed64, 7], [WireType.Fixed32, 3], [WireType.LengthDelimited, 0]] as const) {
    const w = new WireWriter().tag(9, wire);
    if (wire === WireType.LengthDelimited) w.varint(100);
    w.raw(new Uint8Array(avail));
    assert.throws(() => {
      const r = new WireReader(w.finish());
      ; r.tag(); r.skip(wire);
    }, TruncatedError);
  }
});
