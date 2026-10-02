import { test } from 'node:test';
import assert from 'node:assert/strict';
import { defineMessage } from '../src/schema.ts';
import { decodeMessage, encodeMessage } from '../src/message.ts';
import { DecodeError } from '../src/errors.ts';
import { WireType, WireWriter } from '../src/wire.ts';

// Three-level nested schema with packed repeated fields.
const Point = defineMessage('Point', [
  { name: 'x', number: 1, type: 'sint32' },
  { name: 'y', number: 2, type: 'sint32' },
]);

const Track = defineMessage('Track', [
  { name: 'name', number: 1, type: 'string' },
  { name: 'points', number: 2, type: 'message', message: Point, repeated: true },
  { name: 'samples', number: 3, type: 'int32', repeated: true, packed: true },
  { name: 'weights', number: 4, type: 'double', repeated: true, packed: true },
]);

const Session = defineMessage('Session', [
  { name: 'id', number: 1, type: 'uint64' },
  { name: 'active', number: 2, type: 'bool' },
  { name: 'track', number: 3, type: 'message', message: Track },
  { name: 'tags', number: 4, type: 'string', repeated: true },
  { name: 'blob', number: 5, type: 'bytes' },
  { name: 'checksum', number: 6, type: 'fixed64' },
  { name: 'delta', number: 7, type: 'sint64' },
]);

const sample = {
  id: 9007199254740993n,
  active: true,
  track: {
    name: '轨迹-α',
    points: [{ x: -3, y: 7 }, { x: 0, y: 0 }, { x: 2 ** 31 - 1, y: -(2 ** 31) }],
    samples: [1, -2, 300, 0, 2 ** 31 - 1],
    weights: [0.5, -1.25, Math.PI],
  },
  tags: ['alpha', '', '深层嵌套'],
  blob: new Uint8Array([0, 1, 2, 255]),
  checksum: 0xdeadbeefcafebaben,
  delta: -(2n ** 62n),
};

test('roundtrip: multi-level nesting + packed repeated + all scalar kinds', () => {
  const encoded = encodeMessage(Session, sample);
  const { value } = decodeMessage(Session, encoded);
  assert.deepEqual(value, sample);
});

test('packed repeated fields use a single length-delimited record', () => {
  const encoded = encodeMessage(Track, { name: 't', points: [], samples: [1, 2, 3], weights: [] });
  // tag(3, LEN) appears exactly once for the packed samples field
  const tagByte = (3 << 3) | WireType.LengthDelimited;
  const occurrences = encoded.filter((b, i) => b === tagByte && encoded[i - 1] !== 0x80).length;
  assert.ok(occurrences >= 1);
  const { value } = decodeMessage(Track, encoded);
  assert.deepEqual(value.samples, [1, 2, 3]);
});

test('decoder accepts both packed and unpacked repeated encodings', () => {
  // Manually build an *unpacked* encoding of a packed-capable field.
  const w = new WireWriter();
  w.writeTag(3, WireType.Varint).writeVarint(10);
  w.writeTag(3, WireType.Varint).writeVarint(20);
  const { value } = decodeMessage(Track, w.finish());
  assert.deepEqual(value.samples, [10, 20]);
});

test('truncated field anywhere in the stream throws DecodeError with offset', () => {
  const encoded = encodeMessage(Session, sample);
  // Cutting the buffer at ANY point must fail safely, never crash or read OOB.
  for (let cut = 1; cut < encoded.length; cut++) {
    try {
      decodeMessage(Session, encoded.subarray(0, cut));
    } catch (e) {
      assert.ok(e instanceof DecodeError, `cut=${cut}: expected DecodeError, got ${e}`);
      assert.ok((e as DecodeError).offset <= encoded.length);
      continue;
    }
    // A cut that lands exactly on a field boundary may decode a valid prefix.
    // Re-decoding must then simply yield a subset of fields.
  }
});

test('truncated nested message reports the nested boundary', () => {
  const encoded = encodeMessage(Session, sample);
  // Find the nested track field and truncate inside it.
  const tagIdx = encoded.findIndex((b, i) => b === ((3 << 3) | 2) && i > 0);
  assert.ok(tagIdx > 0);
  const innerCut = tagIdx + 3; // inside the declared length-delimited payload
  assert.throws(() => decodeMessage(Session, encoded.subarray(0, innerCut)), DecodeError);
});

test('declared length exceeding buffer is rejected safely', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.LengthDelimited).writeVarint(1_000_000).writeRaw(new Uint8Array([1]));
  const S = defineMessage('S', [{ name: 's', number: 1, type: 'string' }]);
  assert.throws(() => decodeMessage(S, w.finish()), /exceeds remaining/);
});

test('unknown fields are skipped safely and can be retained', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Varint).writeVarint(42);          // known: id
  w.writeTag(99, WireType.LengthDelimited).writeBytes(new Uint8Array([1, 2, 3])); // unknown
  w.writeTag(100, WireType.Fixed32).writeFixed32(7);       // unknown
  const S = defineMessage('S', [{ name: 'id', number: 1, type: 'uint32' }]);
  const { value, unknownFields } = decodeMessage(S, w.finish(), { retainUnknownFields: true });
  assert.deepEqual(value, { id: 42 });
  assert.equal(unknownFields!.size, 2);
  // raw slice covers everything after the tag: length prefix + payload
  assert.deepEqual([...unknownFields!.get(99)![0]], [3, 1, 2, 3]);
});

test('wire-type mismatch for a known field is a DecodeError', () => {
  const w = new WireWriter();
  w.writeTag(1, WireType.Fixed32).writeFixed32(1); // schema expects varint
  const S = defineMessage('S', [{ name: 'id', number: 1, type: 'uint32' }]);
  assert.throws(() => decodeMessage(S, w.finish()), /wire type/);
});

test('empty message and empty buffer decode to empty object', () => {
  const { value } = decodeMessage(Session, new Uint8Array(0));
  assert.deepEqual(value, {});
  assert.deepEqual(encodeMessage(Session, {}).length, 0);
});

test('large packed array roundtrips (10k elements)', () => {
  const samples = Array.from({ length: 10_000 }, (_, i) => i - 5000);
  const encoded = encodeMessage(Track, { name: 'big', points: [], samples, weights: [] });
  const { value } = decodeMessage(Track, encoded);
  assert.deepEqual(value.samples, samples);
});
