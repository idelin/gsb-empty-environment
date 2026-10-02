import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  BufferWriter, DecodeError, ProtoEngine, WireType,
  type MessageDescriptor,
} from '../src/index.ts';

const Inner: MessageDescriptor = {
  name: 'Inner',
  fields: [
    { number: 1, name: 'flag', type: 'bool' },
    { number: 2, name: 'note', type: 'string' },
  ],
};

const Middle: MessageDescriptor = {
  name: 'Middle',
  fields: [
    { number: 1, name: 'inner', type: 'message', message: Inner },
    { number: 2, name: 'tags', type: 'string', repeated: true },
  ],
};

const Outer: MessageDescriptor = {
  name: 'Outer',
  fields: [
    { number: 1, name: 'id', type: 'uint64' },
    { number: 2, name: 'delta', type: 'sint32' },
    { number: 3, name: 'big', type: 'sfixed64' },
    { number: 4, name: 'score', type: 'float' },
    { number: 5, name: 'nums', type: 'sint32', repeated: true, packed: true },
    { number: 6, name: 'middle', type: 'message', message: Middle },
    { number: 7, name: 'blob', type: 'bytes' },
  ],
};

function encodeOuter(): Uint8Array {
  const inner = new BufferWriter()
    .writeTag(1, WireType.Varint).writeVarint(1n)
    .writeTag(2, WireType.LengthDelimited).writeString('deep')
    .finish();
  const middle = new BufferWriter()
    .writeTag(1, WireType.LengthDelimited).writeBytes(inner)
    .writeTag(2, WireType.LengthDelimited).writeString('a')
    .writeTag(2, WireType.LengthDelimited).writeString('b')
    .finish();
  const packed = new BufferWriter();
  for (const n of [0, -1, 1, 300, -2147483648]) packed.writeZigZag32(n);
  return new BufferWriter()
    .writeTag(1, WireType.Varint).writeVarint(0xffffffffffffffffn)
    .writeTag(2, WireType.Varint).writeZigZag32(-77)
    .writeTag(3, WireType.Fixed64).writeFixed64(-42n)
    .writeTag(4, WireType.Fixed32).writeFloat(2.5)
    .writeTag(5, WireType.LengthDelimited).writeBytes(packed.finish())
    .writeTag(6, WireType.LengthDelimited).writeBytes(middle)
    .writeTag(7, WireType.LengthDelimited).writeBytes(Uint8Array.from([0xde, 0xad]))
    .finish();
}

test('decodes multi-level nested message with packed repeated fields', () => {
  const engine = new ProtoEngine();
  const out = engine.decode(Outer, encodeOuter());
  assert.equal(out.ok, true, JSON.stringify(out.error));
  const v = out.value as Record<string, unknown>;
  assert.equal(v.id, 0xffffffffffffffffn);
  assert.equal(v.delta, -77);
  assert.equal(v.big, -42n);
  assert.equal(v.score, 2.5);
  assert.deepEqual(v.nums, [0, -1, 1, 300, -2147483648]);
  assert.deepEqual(v.blob, Uint8Array.from([0xde, 0xad]));
  const middle = v.middle as Record<string, unknown>;
  assert.deepEqual(middle.tags, ['a', 'b']);
  const inner = middle.inner as Record<string, unknown>;
  assert.equal(inner.flag, true);
  assert.equal(inner.note, 'deep');
});

test('accepts packed repeated fields sent in unpacked (per-element) form', () => {
  const w = new BufferWriter()
    .writeTag(5, WireType.Varint).writeZigZag32(5)
    .writeTag(5, WireType.Varint).writeZigZag32(-6)
    .finish();
  const out = new ProtoEngine().decode(Outer, w);
  assert.equal(out.ok, true);
  assert.deepEqual((out.value as Record<string, unknown>).nums, [5, -6]);
});

test('skips unknown fields of every wire type safely', () => {
  const w = new BufferWriter()
    .writeTag(100, WireType.Varint).writeVarint(9n)
    .writeTag(101, WireType.Fixed64).writeFixed64(1n)
    .writeTag(102, WireType.LengthDelimited).writeString('skip me')
    .writeTag(103, WireType.Fixed32).writeFixed32(7)
    .writeTag(1, WireType.Varint).writeVarint(3n)
    .finish();
  const engine = new ProtoEngine();
  const out = engine.decode(Outer, w);
  assert.equal(out.ok, true);
  assert.equal((out.value as Record<string, unknown>).id, 3n);
  assert.equal(engine.metrics.getCounter('decode_unknown_fields'), 4);
});

test('empty stream is a valid empty message', () => {
  const out = new ProtoEngine().decode(Outer, new Uint8Array(0));
  assert.equal(out.ok, true);
  assert.deepEqual(out.value, {});
});

test('every truncation point either decodes cleanly or fails with a bounded DecodeError', () => {
  const full = encodeOuter();
  const engine = new ProtoEngine();
  let failures = 0;
  let boundaryOk = 0;
  for (let len = 1; len < full.byteLength; len++) {
    const out = engine.decode(Outer, full.subarray(0, len));
    if (out.ok) {
      boundaryOk++; // prefix ended exactly on a field boundary: valid shorter message
    } else {
      assert.ok(out.error instanceof DecodeError, `prefix ${len} threw non-DecodeError`);
      assert.match(out.error.code, /^ERR_/);
      failures++;
    }
  }
  assert.ok(failures > 0, 'expected mid-field truncations to be caught');
  assert.ok(boundaryOk > 0, 'expected some prefixes to land on field boundaries');
  assert.equal(failures + boundaryOk, full.byteLength - 1);
  assert.equal(engine.metrics.getCounter('decode_errors_total'), failures);
});

test('truncated length-delimited payload reports ERR_TRUNCATED with field number', () => {
  // field 7 (bytes) declares length 10 but the buffer ends immediately.
  const buf = new BufferWriter().writeTag(7, WireType.LengthDelimited).writeVarint(10n).finish();
  const out = new ProtoEngine().decode(Outer, buf);
  assert.equal(out.ok, false);
  assert.equal(out.error?.code, 'ERR_TRUNCATED');
  assert.equal(out.error?.fieldNumber, 7);
});

test('wire type mismatch is rejected', () => {
  const buf = new BufferWriter().writeTag(1, WireType.Fixed32).writeFixed32(1).finish();
  const out = new ProtoEngine().decode(Outer, buf);
  assert.equal(out.error?.code, 'ERR_WIRE_TYPE_MISMATCH');
});

test('field number 0 tag is rejected', () => {
  const out = new ProtoEngine().decode(Outer, Uint8Array.from([0x00]));
  assert.equal(out.error?.code, 'ERR_INVALID_TAG');
});

test('invalid UTF-8 in string field is reported', () => {
  const w = new BufferWriter()
    .writeTag(2, WireType.LengthDelimited).writeBytes(Uint8Array.from([0xff, 0xfe]))
    .finish();
  const mid = new ProtoEngine().decode(Middle, w);
  assert.equal(mid.error?.code, 'ERR_INVALID_UTF8');
});

test('nesting beyond depth limit is rejected', () => {
  const Node: MessageDescriptor = { name: 'Node', fields: [] };
  Node.fields.push({ number: 1, name: 'next', type: 'message', message: Node });
  let payload = new Uint8Array(0);
  for (let i = 0; i < 40; i++) {
    payload = new BufferWriter().writeTag(1, WireType.LengthDelimited).writeBytes(payload).finish();
  }
  const out = new ProtoEngine({ maxDepth: 8 }).decode(Node, payload);
  assert.equal(out.error?.code, 'ERR_DEPTH_LIMIT');
});

test('truncated packed payload reports the owning field', () => {
  // packed nums declares 9 bytes but only 3 are present.
  const buf = new BufferWriter()
    .writeTag(5, WireType.LengthDelimited).writeVarint(9n)
    .writeByte(1).writeByte(2).writeByte(3)
    .finish();
  const out = new ProtoEngine().decode(Outer, buf);
  assert.equal(out.error?.code, 'ERR_TRUNCATED');
  assert.equal(out.error?.fieldNumber, 5);
});

test('corrupt packed element (truncated varint inside packed body) is caught', () => {
  // packed body ends with a continuation byte and no follow-up.
  const buf = new BufferWriter()
    .writeTag(5, WireType.LengthDelimited).writeVarint(2n)
    .writeByte(0x01).writeByte(0x80)
    .finish();
  const out = new ProtoEngine().decode(Outer, buf);
  assert.equal(out.error?.code, 'ERR_TRUNCATED');
  assert.equal(out.error?.fieldNumber, 5);
});
