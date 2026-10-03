import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeAndValidate,
  decodeMessage,
  encodeMessage,
  FieldType,
  SchemaError,
  validateMessage,
  ValidationError,
  type MessageSchema,
} from '../src/schema.ts';
import { TruncatedFieldError } from '../src/varint.ts';
import { WireType, WireWriter } from '../src/wire.ts';

// message Inner { sint32 delta = 1; }
const Inner: MessageSchema = {
  name: 'Inner',
  fields: [{ fieldNumber: 1, name: 'delta', type: FieldType.Sint32, required: true }],
};

// message Middle { Inner inner = 1; repeated fixed64 samples = 2 [packed]; }
const Middle: MessageSchema = {
  name: 'Middle',
  fields: [
    { fieldNumber: 1, name: 'inner', type: FieldType.Message, messageType: Inner },
    { fieldNumber: 2, name: 'samples', type: FieldType.Fixed64, repeated: true, packed: true },
  ],
};

// message Outer {
//   string name = 1; uint32 id = 2; Middle mid = 3;
//   repeated sint32 scores = 4 [packed]; bool active = 5; bytes blob = 6;
// }
const Outer: MessageSchema = {
  name: 'Outer',
  fields: [
    { fieldNumber: 1, name: 'name', type: FieldType.String, required: true },
    { fieldNumber: 2, name: 'id', type: FieldType.Uint32 },
    { fieldNumber: 3, name: 'mid', type: FieldType.Message, messageType: Middle },
    { fieldNumber: 4, name: 'scores', type: FieldType.Sint32, repeated: true, packed: true },
    { fieldNumber: 5, name: 'active', type: FieldType.Bool },
    { fieldNumber: 6, name: 'blob', type: FieldType.Bytes },
  ],
};

const sample = {
  name: 'node-7',
  id: 4242,
  mid: {
    inner: { delta: -17 },
    samples: [1n, 2n ** 63n, 2n ** 64n - 1n],
  },
  scores: [-5, 0, 7, -300, 123456],
  active: true,
  blob: new Uint8Array([0xde, 0xad, 0xbe, 0xef]),
};

test('round-trips a 3-level nested message with packed repeated fields', () => {
  const buf = encodeMessage(Outer, sample);
  const decoded = decodeMessage(Outer, buf);
  assert.deepEqual(decoded, sample);
  assert.deepEqual(validateMessage(Outer, decoded), []);
});

test('decodes a hand-crafted binary stream byte-for-byte', () => {
  // Built manually: field 1 varint 1; field 2 length-delimited { field 1 varint 150 }
  const S: MessageSchema = {
    name: 'S',
    fields: [
      { fieldNumber: 1, name: 'a', type: FieldType.Uint32 },
      { fieldNumber: 2, name: 'sub', type: FieldType.Message, messageType: Inner },
    ],
  };
  const InnerAsVarint: MessageSchema = {
    name: 'InnerAsVarint',
    fields: [{ fieldNumber: 1, name: 'n', type: FieldType.Uint32 }],
  };
  const S2: MessageSchema = {
    name: 'S2',
    fields: [
      { fieldNumber: 1, name: 'a', type: FieldType.Uint32 },
      { fieldNumber: 2, name: 'sub', type: FieldType.Message, messageType: InnerAsVarint },
    ],
  };
  // bytes: 08 01 | 12 03 08 96 01
  const raw = new Uint8Array([0x08, 0x01, 0x12, 0x03, 0x08, 0x96, 0x01]);
  const decoded = decodeMessage(S2, raw);
  assert.deepEqual(decoded, { a: 1, sub: { n: 150 } });
  void S;
});

test('packed repeated varints decode element-by-element', () => {
  // scores = [-5, 0, 7] zigzag -> [9, 0, 14] packed: 22 03 09 00 0e
  const raw = new Uint8Array([0x22, 0x03, 0x09, 0x00, 0x0e]);
  const decoded = decodeMessage(Outer, raw);
  assert.deepEqual(decoded.scores, [-5, 0, 7]);
});

test('packed payload truncated mid-element is a safe error', () => {
  // declares 3 packed bytes but only 2 present
  const raw = new Uint8Array([0x22, 0x03, 0x09, 0x00]);
  assert.throws(() => decodeMessage(Outer, raw), TruncatedFieldError);
  // packed varint cut in half: payload length 1, byte 0x80 (continuation, no next)
  const broken = new Uint8Array([0x22, 0x01, 0x80]);
  assert.throws(() => decodeMessage(Outer, broken), TruncatedFieldError);
});

test('nested message prefixes never cause out-of-bounds reads', () => {
  const full = encodeMessage(Outer, sample);
  for (let len = 0; len < full.length; len++) {
    try {
      // a prefix ending exactly on a field boundary is a valid shorter message
      decodeMessage(Outer, full.subarray(0, len));
    } catch (err) {
      assert.ok(
        err instanceof TruncatedFieldError,
        `prefix ${len}/${full.length} must fail with TruncatedFieldError, got ${err}`,
      );
    }
  }
});

test('unknown fields are skipped without disturbing known ones', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeString('keep');
  w.writeTag(99, WireType.Varint).writeVarint(123n); // unknown
  w.writeTag(100, WireType.LengthDelimited).writeString('unknown'); // unknown
  w.writeTag(2, WireType.Varint).writeVarint(7);
  const decoded = decodeMessage(Outer, w.finish());
  assert.equal(decoded.name, 'keep');
  assert.equal(decoded.id, 7);
  assert.equal(decoded['99'], undefined);
});

test('wire-type mismatch against schema is a SchemaError', () => {
  // field 1 (string) encoded as varint
  const raw = new Uint8Array([0x08, 0x01]);
  assert.throws(() => decodeMessage(Outer, raw), SchemaError);
});

test('duplicate field numbers in a schema are rejected', () => {
  const bad: MessageSchema = {
    name: 'Bad',
    fields: [
      { fieldNumber: 1, name: 'a', type: FieldType.Uint32 },
      { fieldNumber: 1, name: 'b', type: FieldType.Uint32 },
    ],
  };
  assert.throws(() => decodeMessage(bad, new Uint8Array(0)), SchemaError);
});

test('validation engine reports every violation with full paths', () => {
  const violations = validateMessage(Outer, {
    // name missing (required)
    id: -3, // uint32 out of range
    mid: { inner: { delta: 'oops' }, samples: [1n, 'x'] },
    scores: [1, 2.5],
    active: 'yes',
    blob: 'not-bytes',
  } as never);
  const text = violations.join('\n');
  assert.match(text, /Outer\.name: required field is missing/);
  assert.match(text, /Outer\.id: out of uint32 range/);
  assert.match(text, /Outer\.mid\.inner\.delta: expected integer/);
  assert.match(text, /Outer\.mid\.samples\[1\]: expected bigint/);
  assert.match(text, /Outer\.scores\[1\]: expected integer/);
  assert.match(text, /Outer\.active: expected boolean/);
  assert.match(text, /Outer\.blob: expected Uint8Array/);
  assert.equal(violations.length, 7);
});

test('decodeAndValidate throws ValidationError aggregating violations', () => {
  const buf = encodeMessage(Outer, { id: 5 } as never); // missing required name
  assert.throws(() => decodeAndValidate(Outer, buf), (err: unknown) => {
    assert.ok(err instanceof ValidationError);
    assert.equal(err.violations.length, 1);
    assert.match(err.violations[0], /name: required field is missing/);
    return true;
  });
});

test('int64/uint64 boundaries survive the round trip as bigint', () => {
  const S: MessageSchema = {
    name: 'Big',
    fields: [
      { fieldNumber: 1, name: 'i', type: FieldType.Int64 },
      { fieldNumber: 2, name: 'u', type: FieldType.Uint64 },
      { fieldNumber: 3, name: 's', type: FieldType.Sint64 },
    ],
  };
  const obj = { i: -(2n ** 63n), u: 2n ** 64n - 1n, s: 2n ** 63n - 1n };
  assert.deepEqual(decodeMessage(S, encodeMessage(S, obj)), obj);
  assert.deepEqual(validateMessage(S, obj), []);
  assert.equal(validateMessage(S, { ...obj, u: 2n ** 64n }).length, 1);
});
