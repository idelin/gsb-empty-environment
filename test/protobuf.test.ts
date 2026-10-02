import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeMessage,
  decodeVarint,
  encodeMessage,
  encodeVarint,
  ProtobufTruncationError,
  ProtobufValidationError,
  ProtobufWireError,
  Reader,
  WireType,
  Writer,
  zigzagDecode32,
  zigzagDecode64,
  zigzagEncode32,
  zigzagEncode64,
} from '../src/protobuf/index.ts';
import type { MessageDescriptor } from '../src/protobuf/index.ts';

// ---------------------------------------------------------------- varint --

test('varint: round-trips boundary values', () => {
  const cases: Array<[bigint, number[]]> = [
    [0n, [0x00]],
    [1n, [0x01]],
    [127n, [0x7f]],
    [128n, [0x80, 0x01]],
    [300n, [0xac, 0x02]],
    [16384n, [0x80, 0x80, 0x01]],
    [(1n << 32n) - 1n, [0xff, 0xff, 0xff, 0xff, 0x0f]],
    [(1n << 64n) - 1n, [0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01]],
  ];
  for (const [value, bytes] of cases) {
    const encoded = encodeVarint(value);
    assert.deepEqual([...encoded], bytes, `encode ${value}`);
    const decoded = decodeVarint(Uint8Array.from(bytes), 0);
    assert.equal(decoded.value, value, `decode ${value}`);
    assert.equal(decoded.offset, bytes.length);
  }
});

test('varint: truncated buffer raises ProtobufTruncationError, never reads OOB', () => {
  // 0x80 promises a continuation byte that never arrives.
  assert.throws(() => decodeVarint(Uint8Array.from([0x80]), 0), ProtobufTruncationError);
  assert.throws(
    () => decodeVarint(Uint8Array.from([0xff, 0xff, 0xff]), 0),
    ProtobufTruncationError
  );
});

test('varint: rejects encodings longer than 64 bits', () => {
  const tooLong = Uint8Array.from([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f]);
  assert.throws(() => decodeVarint(tooLong, 0), ProtobufWireError);
  const eleven = Uint8Array.from([0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01]);
  assert.throws(() => decodeVarint(eleven, 0), ProtobufWireError);
});

// ---------------------------------------------------------------- zigzag --

test('zigzag32: maps signed values to small unsigned magnitudes', () => {
  const cases: Array<[number, number]> = [
    [0, 0], [-1, 1], [1, 2], [-2, 3], [2, 4],
    [2147483647, 4294967294],
    [-2147483648, 4294967295],
  ];
  for (const [signed, encoded] of cases) {
    assert.equal(zigzagEncode32(signed), encoded);
    assert.equal(zigzagDecode32(encoded), signed);
  }
});

test('zigzag64: round-trips 64-bit extremes', () => {
  const cases: Array<[bigint, bigint]> = [
    [0n, 0n], [-1n, 1n], [1n, 2n], [-2n, 3n],
    [(1n << 63n) - 1n, (1n << 64n) - 2n],
    [-(1n << 63n), (1n << 64n) - 1n],
  ];
  for (const [signed, encoded] of cases) {
    assert.equal(zigzagEncode64(signed), encoded);
    assert.equal(zigzagDecode64(encoded), signed);
  }
});

// ------------------------------------------------------ fixed32 / fixed64 --

test('fixed32/fixed64: little-endian round-trip and truncation guard', () => {
  const w = new Writer();
  w.fixed32(0xdeadbeef).sfixed32(-123456).fixed64(0x0102030405060708n).sfixed64(-42n);
  const r = new Reader(w.finish());
  assert.equal(r.fixed32(), 0xdeadbeef);
  assert.equal(r.sfixed32(), -123456);
  assert.equal(r.fixed64(), 0x0102030405060708n);
  assert.equal(r.sfixed64(), -42n);
  assert.ok(r.eof);

  assert.throws(() => new Reader(Uint8Array.from([1, 2, 3])).fixed32(), ProtobufTruncationError);
  assert.throws(() => new Reader(Uint8Array.from([1, 2, 3, 4, 5])).fixed64(), ProtobufTruncationError);
});

test('float/double round-trip', () => {
  const w = new Writer();
  w.float(1.5).double(-3.141592653589793);
  const r = new Reader(w.finish());
  assert.equal(r.float(), 1.5);
  assert.equal(r.double(), -3.141592653589793);
});

// ------------------------------------------------- reflection-style codec --

const Address: MessageDescriptor = {
  name: 'Address',
  fields: [
    { name: 'street', number: 1, type: 'string' },
    { name: 'zip', number: 2, type: 'uint32' },
  ],
};

const Person: MessageDescriptor = {
  name: 'Person',
  fields: [
    { name: 'id', number: 1, type: 'int32' },
    { name: 'name', number: 2, type: 'string' },
    { name: 'email', number: 3, type: 'string' },
    { name: 'address', number: 4, type: Address },
    { name: 'luckyNumbers', number: 5, type: 'sint32', repeated: true, packed: true },
    { name: 'scores', number: 6, type: 'sint64', repeated: true, packed: true },
    { name: 'active', number: 7, type: 'bool' },
    { name: 'balance', number: 8, type: 'int64' },
    { name: 'avatar', number: 9, type: 'bytes' },
    { name: 'ratio', number: 10, type: 'double' },
  ],
};

const AddressBook: MessageDescriptor = {
  name: 'AddressBook',
  fields: [{ name: 'people', number: 1, type: Person, repeated: true }],
};

function samplePerson() {
  return {
    id: -7,
    name: 'Ada Lovelace',
    email: 'ada@example.com',
    address: { street: '12 Analytical Engine Rd', zip: 10001 },
    luckyNumbers: [0, -1, 1, -2, 300, -2147483648, 2147483647],
    scores: [0n, -1n, 1n, (1n << 63n) - 1n, -(1n << 63n)],
    active: true,
    balance: -9_000_000_000n,
    avatar: Uint8Array.from([0xde, 0xad, 0xbe, 0xef]),
    ratio: 0.625,
  };
}

test('schema: multi-level nested message round-trip', () => {
  const book = { people: [samplePerson(), { ...samplePerson(), id: 42, name: 'Bob' }] };
  const encoded = encodeMessage(AddressBook, book);
  const decoded = decodeMessage(AddressBook, encoded) as typeof book;

  assert.equal(decoded.people.length, 2);
  const ada = decoded.people[0];
  assert.equal(ada.id, -7);
  assert.equal(ada.name, 'Ada Lovelace');
  assert.equal(ada.address.street, '12 Analytical Engine Rd');
  assert.equal(ada.address.zip, 10001);
  assert.deepEqual(ada.luckyNumbers, [0, -1, 1, -2, 300, -2147483648, 2147483647]);
  assert.deepEqual(ada.scores, [0n, -1n, 1n, (1n << 63n) - 1n, -(1n << 63n)]);
  assert.equal(ada.active, true);
  assert.equal(ada.balance, -9_000_000_000n);
  assert.deepEqual([...ada.avatar], [0xde, 0xad, 0xbe, 0xef]);
  assert.equal(ada.ratio, 0.625);
  assert.equal(decoded.people[1].id, 42);
});

test('schema: packed repeated fields use a single length-delimited block', () => {
  const encoded = encodeMessage(Person, { luckyNumbers: [1, 2, 3] });
  // tag(5, len-delim)=0x2a, len=3, then three 1-byte zigzag varints.
  assert.deepEqual([...encoded], [0x2a, 0x03, 0x02, 0x04, 0x06]);
  const decoded = decodeMessage(Person, encoded);
  assert.deepEqual(decoded.luckyNumbers, [1, 2, 3]);
});

test('schema: decoder accepts both packed and unpacked repeated encodings', () => {
  const packed = encodeMessage(Person, { luckyNumbers: [5, -5] });
  // Same values encoded as two separate varint fields (tag 0x28).
  const unpacked = Uint8Array.from([0x28, 0x0a, 0x28, 0x09]);
  assert.deepEqual(decodeMessage(Person, packed).luckyNumbers, [5, -5]);
  assert.deepEqual(decodeMessage(Person, unpacked).luckyNumbers, [5, -5]);
});

test('schema: unknown fields are skipped safely', () => {
  const w = new Writer();
  w.tag(1, WireType.Varint).int32(7); // known: id
  w.tag(99, WireType.LengthDelimited).bytes(Uint8Array.from([1, 2, 3])); // unknown
  w.tag(100, WireType.Fixed64).fixed64(1n); // unknown
  w.tag(101, WireType.Fixed32).fixed32(1); // unknown
  w.tag(102, WireType.Varint).varint(12345); // unknown
  const decoded = decodeMessage(Person, w.finish());
  assert.equal(decoded.id, 7);
  assert.equal(decoded.name, undefined);
});

test('schema: negative int32 encodes as 10-byte varint and decodes back', () => {
  const encoded = encodeMessage(Person, { id: -1 });
  assert.equal(encoded.length, 1 + 10);
  assert.equal(decodeMessage(Person, encoded).id, -1);
});

// ------------------------------------------------------- validation engine --

test('validation: rejects wrong value types and out-of-range integers', () => {
  assert.throws(() => encodeMessage(Person, { id: 'nope' }), ProtobufValidationError);
  assert.throws(() => encodeMessage(Person, { id: 2 ** 31 }), ProtobufValidationError);
  assert.throws(() => encodeMessage(Person, { id: -(2 ** 31) - 1 }), ProtobufValidationError);
  assert.throws(() => encodeMessage(Person, { active: 1 }), ProtobufValidationError);
  assert.throws(() => encodeMessage(Person, { name: 42 }), ProtobufValidationError);
  assert.throws(() => encodeMessage(Person, { avatar: [1, 2] }), ProtobufValidationError);
  assert.throws(() => encodeMessage(Person, { balance: 1n << 63n }), ProtobufValidationError);
  assert.throws(
    () => encodeMessage(Person, { luckyNumbers: [1, 'x'] }),
    ProtobufValidationError
  );
  assert.throws(
    () => encodeMessage(Person, { luckyNumbers: 5 }),
    ProtobufValidationError
  );
});

test('validation: rejects wire-type mismatches', () => {
  // Field 1 (id) declared varint, but encoded as fixed32.
  const w = new Writer();
  w.tag(1, WireType.Fixed32).fixed32(7);
  assert.throws(() => decodeMessage(Person, w.finish()), ProtobufWireError);
});

// ------------------------------------------------------------ truncation --

test('truncation: every cut point of a nested message fails safely', () => {
  const full = encodeMessage(AddressBook, { people: [samplePerson()] });
  // The trailing field is `ratio` (double): 1 tag byte + 8 payload bytes.
  const lastFieldStart = full.length - 9;
  for (let cut = 0; cut < full.length; cut++) {
    let threwSafely = false;
    try {
      decodeMessage(AddressBook, full.subarray(0, cut));
    } catch (err) {
      assert.ok(
        err instanceof ProtobufTruncationError || err instanceof ProtobufWireError,
        `cut=${cut}: expected safe error, got ${err}`
      );
      threwSafely = true;
    }
    // A cut may only decode cleanly at an exact field boundary (a valid
    // prefix message); every cut inside the trailing field must throw.
    if (cut > lastFieldStart) {
      assert.ok(threwSafely, `cut=${cut} inside final field must throw`);
    }
  }
  // Sanity: the full buffer decodes fine.
  assert.ok(decodeMessage(AddressBook, full));
});

test('truncation: length-delimited field claiming more bytes than available', () => {
  // tag(2, len-delim) then length 100 but only 2 payload bytes.
  const buf = Uint8Array.from([0x12, 0x64, 0x61, 0x62]);
  assert.throws(() => decodeMessage(Person, buf), ProtobufTruncationError);
});

test('truncation: nested message cut mid-header', () => {
  // tag(4, len-delim) then a varint length whose continuation byte is missing.
  const buf = Uint8Array.from([0x22, 0x80]);
  assert.throws(() => decodeMessage(Person, buf), ProtobufTruncationError);
});
