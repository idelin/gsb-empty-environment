import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ProtobufDecodeError } from '../src/encoding.ts';
import { ProtoReader, WireType } from '../src/reader.ts';
import { ProtoWriter } from '../src/writer.ts';
import { MessageSchema, SchemaValidationError } from '../src/schema.ts';

const Address = new MessageSchema('Address', [
  { name: 'street', number: 1, type: 'string', required: true },
  { name: 'zip', number: 2, type: 'sint32' },
]);

const Person = new MessageSchema('Person', [
  { name: 'id', number: 1, type: 'uint64', required: true },
  { name: 'name', number: 2, type: 'string', required: true },
  { name: 'email', number: 3, type: 'string' },
  { name: 'scores', number: 4, type: 'sint32', repeated: true, packed: true },
  { name: 'tags', number: 5, type: 'string', repeated: true },
  { name: 'address', number: 6, type: Address },
  { name: 'history', number: 7, type: Address, repeated: true },
  { name: 'balance', number: 8, type: 'sfixed64' },
  { name: 'active', number: 9, type: 'bool' },
  { name: 'ratio', number: 10, type: 'double' },
  { name: 'checksum', number: 11, type: 'fixed32' },
  { name: 'blob', number: 12, type: 'bytes' },
]);

const sample = {
  id: 18446744073709551615n,
  name: 'Ada Lovelace',
  email: 'ada@analytical.engine',
  scores: [-5, 0, 7, 2147483647, -2147483648],
  tags: ['admin', 'founder'],
  address: { street: '12 Analytical Way', zip: -94107 },
  history: [
    { street: '1 First St', zip: 10001 },
    { street: '2 Second Ave', zip: -20002 },
  ],
  balance: -9223372036854775808n,
  active: true,
  ratio: 3.141592653589793,
  checksum: 0xdeadbeef,
  blob: new Uint8Array([0, 1, 2, 253, 254, 255]),
};

test('schema roundtrip: nested messages, packed repeated, all scalar kinds', () => {
  const bytes = Person.encode(sample);
  const decoded = Person.decode(bytes);
  assert.deepEqual(decoded, sample);
});

test('handcrafted wire stream decodes to the same structure', () => {
  const w = new ProtoWriter();
  w.writeVarintField(1, 0xffffffffffffffffn);
  w.writeStringField(2, 'Ada Lovelace');
  w.writeStringField(3, 'ada@analytical.engine');
  w.writePackedVarintField(4, [9, 0, 14, 0xfffffffe, 0xffffffff]);
  w.writeStringField(5, 'admin');
  w.writeStringField(5, 'founder');
  const addr = new ProtoWriter();
  addr.writeStringField(1, '12 Analytical Way');
  addr.writeZigZagField(2, -94107n);
  w.writeMessageField(6, addr.finish());
  const h1 = new ProtoWriter();
  h1.writeStringField(1, '1 First St');
  h1.writeZigZagField(2, 10001n);
  w.writeMessageField(7, h1.finish());
  const h2 = new ProtoWriter();
  h2.writeStringField(1, '2 Second Ave');
  h2.writeZigZagField(2, -20002n);
  w.writeMessageField(7, h2.finish());
  w.writeFixed64Field(8, 0x8000000000000000n);
  w.writeVarintField(9, 1);
  w.writeTag(10, WireType.Fixed64).writeDouble(3.141592653589793);
  w.writeFixed32Field(11, 0xdeadbeef);
  w.writeBytesField(12, new Uint8Array([0, 1, 2, 253, 254, 255]));

  const decoded = Person.decode(w.finish());
  assert.deepEqual(decoded, sample);
});

test('packed repeated also accepts unpacked element-by-element encoding', () => {
  const w = new ProtoWriter();
  w.writeVarintField(1, 7n);
  w.writeStringField(2, 'Grace');
  for (const s of [3, -4, 5]) {
    w.writeZigZagField(4, BigInt(s));
  }
  const decoded = Person.decode(w.finish());
  assert.deepEqual(decoded.scores, [3, -4, 5]);
});

test('unknown fields are skipped without disturbing known fields', () => {
  const w = new ProtoWriter();
  w.writeVarintField(1, 42n);
  w.writeVarintField(99, 123456789n);
  w.writeStringField(2, 'Hopper');
  w.writeFixed64Field(100, 0x1122334455667788n);
  w.writeBytesField(101, new Uint8Array([9, 9, 9]));
  w.writeFixed32Field(102, 7);
  const decoded = Person.decode(w.finish());
  assert.deepEqual(decoded, { id: 42n, name: 'Hopper' });
});

test('three-level nesting decodes correctly', () => {
  const Inner = new MessageSchema('Inner', [
    { name: 'value', number: 1, type: 'sint64', required: true },
  ]);
  const Mid = new MessageSchema('Mid', [
    { name: 'inner', number: 1, type: Inner, required: true },
    { name: 'flags', number: 2, type: 'bool', repeated: true, packed: true },
  ]);
  const Outer = new MessageSchema('Outer', [
    { name: 'mid', number: 1, type: Mid, required: true },
  ]);
  const obj = { mid: { inner: { value: -9223372036854775808n }, flags: [true, false, true] } };
  assert.deepEqual(Outer.decode(Outer.encode(obj)), obj);
});

test('truncation mid-varint reports safe bounds error', () => {
  const bytes = Person.encode(sample);
  const idEnd = bytes.indexOf(0x12);
  assert.ok(idEnd > 0);
  for (let cut = 1; cut < idEnd; cut++) {
    assert.throws(
      () => Person.decode(bytes.subarray(0, cut)),
      ProtobufDecodeError,
      `cut at ${cut}`,
    );
  }
});

test('truncation mid-string and mid-message reports safe bounds error', () => {
  const bytes = Person.encode(sample);
  const nameTag = bytes.indexOf(0x12);
  const nameEnd = nameTag + 2 + 'Ada Lovelace'.length;
  for (const cut of [nameTag + 1, nameTag + 2, nameEnd - 1]) {
    assert.throws(
      () => Person.decode(bytes.subarray(0, cut)),
      ProtobufDecodeError,
      `cut at ${cut}`,
    );
  }
});

test('declared length exceeding remaining buffer is rejected', () => {
  const w = new ProtoWriter();
  w.writeVarintField(1, 1n);
  w.writeTag(2, WireType.LengthDelimited);
  w.writeVarint(0xffff);
  w.writeRawBytes(new Uint8Array([0x41, 0x42]));
  assert.throws(() => Person.decode(w.finish()), /truncated length-delimited/);
});

test('truncation sweep never throws foreign error types', () => {
  const bytes = Person.encode(sample);
  for (let cut = 0; cut < bytes.length; cut++) {
    try {
      Person.decode(bytes.subarray(0, cut));
    } catch (err) {
      assert.ok(
        err instanceof ProtobufDecodeError || err instanceof SchemaValidationError,
        `cut=${cut} threw ${(err as Error).constructor.name}: ${(err as Error).message}`,
      );
    }
  }
});

test('truncated nested message surfaces field path', () => {
  const bytes = Person.encode(sample);
  const addrTag = bytes.indexOf(0x32);
  assert.ok(addrTag > 0);
  const cut = addrTag + 4;
  assert.throws(
    () => Person.decode(bytes.subarray(0, cut)),
    (err: unknown) =>
      err instanceof ProtobufDecodeError && err.message.includes('Person.address'),
  );
});

test('wire type mismatch is a schema validation error', () => {
  const w = new ProtoWriter();
  w.writeVarintField(1, 1n);
  w.writeFixed32Field(2, 0x41414141);
  assert.throws(() => Person.decode(w.finish()), SchemaValidationError);
});

test('missing required field is reported', () => {
  const w = new ProtoWriter();
  w.writeStringField(2, 'NoId');
  assert.throws(
    () => Person.decode(w.finish()),
    (err: unknown) =>
      err instanceof SchemaValidationError && err.message.includes('Person.id'),
  );
  assert.throws(() => Person.encode({ name: 'x' }), SchemaValidationError);
});

test('encode validates value types strictly', () => {
  assert.throws(
    () => Person.encode({ id: 1n, name: 'x', scores: ['nan'] }),
    SchemaValidationError,
  );
  assert.throws(
    () => Person.encode({ id: 1n, name: 'x', active: 1 }),
    SchemaValidationError,
  );
  assert.throws(
    () => Person.encode({ id: 1n, name: 'x', blob: [1, 2] }),
    SchemaValidationError,
  );
  assert.throws(
    () => Person.encode({ id: 1n, name: 'x', scores: 5 }),
    SchemaValidationError,
  );
});

test('schema rejects invalid definitions', () => {
  assert.throws(
    () => new MessageSchema('Bad', [{ name: 'a', number: 0, type: 'int32' }]),
    SchemaValidationError,
  );
  assert.throws(
    () =>
      new MessageSchema('Bad', [
        { name: 'a', number: 1, type: 'int32' },
        { name: 'b', number: 1, type: 'int32' },
      ]),
    SchemaValidationError,
  );
  assert.throws(
    () =>
      new MessageSchema('Bad', [
        { name: 'a', number: 1, type: 'string', repeated: true, packed: true },
      ]),
    SchemaValidationError,
  );
});

test('reader rejects invalid tags and group wire types', () => {
  const zeroField = new ProtoReader(new Uint8Array([0x00]));
  assert.throws(() => zeroField.readTag(), ProtobufDecodeError);
  const group = new ProtoReader(new Uint8Array([0x0b]));
  assert.throws(() => group.readTag(), /groups are not supported/);
  const badWire = new ProtoReader(new Uint8Array([0x0f]));
  assert.throws(() => badWire.readTag(), ProtobufDecodeError);
});

test('fixed32/fixed64/float/double boundary values roundtrip', () => {
  const S = new MessageSchema('Nums', [
    { name: 'f32', number: 1, type: 'fixed32' },
    { name: 'f64', number: 2, type: 'fixed64' },
    { name: 'flt', number: 3, type: 'float' },
    { name: 'dbl', number: 4, type: 'double' },
  ]);
  const obj = {
    f32: 0xffffffff,
    f64: 0xffffffffffffffffn,
    flt: 1.5,
    dbl: Number.MAX_VALUE,
  };
  assert.deepEqual(S.decode(S.encode(obj)), obj);
});
