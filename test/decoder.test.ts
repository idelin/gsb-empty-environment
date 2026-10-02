import { test } from 'node:test';
import assert from 'node:assert/strict';
import { decodeMessage, encodeMessage, WireError, WireWriter, type MessageDescriptor } from '../src/index.ts';
import { Person, Address, samplePerson } from '../fixtures/schemas.ts';

test('decodes multi-level nested message with packed repeated fields', () => {
  const bytes = encodeMessage(Person, samplePerson);
  const decoded = decodeMessage(Person, bytes) as typeof samplePerson;
  assert.equal(decoded.name, 'Ada');
  assert.equal(decoded.id, 9007199254740993n);
  assert.deepEqual(decoded.scores, [-7, 0, 42, 300, -2147483648, 2147483647]);
  assert.deepEqual(decoded.address, { city: '上海', zip: -200000, geo: { lat: 31.2304, lng: 121.4737 } });
  assert.deepEqual(decoded.aliases, ['ada', 'a.lovelace', '']);
  assert.equal(decoded.balance, -123456789012345n);
  assert.equal(decoded.lucky, 18446744073709551615n);
  assert.deepEqual(decoded.flags, [true, false, true]);
  assert.equal((decoded.previous as unknown[]).length, 2);
  assert.deepEqual(decoded.samples, [0, 4294967295, 16777216]);
});

test('hand-crafted wire bytes decode to expected structure', () => {
  // Person { name: "Bo", scores: [1, -1] packed, address: { city: "BJ" } }
  const w = new WireWriter();
  w.writeTag(1, 2); w.writeString('Bo');
  w.writeTag(3, 2);
  const packed = new WireWriter();
  packed.writeVarint(2);  // zigzag(1)
  packed.writeVarint(1);  // zigzag(-1)
  w.writeLengthDelimited(packed.finish());
  const addr = new WireWriter();
  addr.writeTag(1, 2); addr.writeString('BJ');
  w.writeTag(4, 2); w.writeLengthDelimited(addr.finish());
  const decoded = decodeMessage(Person, w.finish());
  assert.deepEqual(decoded, { name: 'Bo', scores: [1, -1], address: { city: 'BJ' } });
});

test('repeated scalars accept both packed and unpacked encodings', () => {
  const w = new WireWriter();
  w.writeTag(3, 0); w.writeVarint(2);   // scores: 1 (unpacked)
  w.writeTag(3, 0); w.writeVarint(1);   // scores: -1 (unpacked)
  const packed = new WireWriter();
  packed.writeVarint(4);                // scores: 2
  w.writeTag(3, 2); w.writeLengthDelimited(packed.finish());
  const decoded = decodeMessage(Person, w.finish());
  assert.deepEqual(decoded.scores, [1, -1, 2]);
});

test('every truncation point is safe: clean decode at field boundary, else TRUNCATED', () => {
  const bytes = encodeMessage(Person, samplePerson);
  let truncations = 0;
  let cleanCuts = 0;
  for (let i = 0; i < bytes.length; i++) {
    try {
      decodeMessage(Person, bytes.subarray(0, i));
      cleanCuts++; // cut landed exactly on a field boundary: valid shorter message
    } catch (err) {
      assert.ok(err instanceof WireError, `expected WireError at length ${i}, got ${err}`);
      assert.equal((err as WireError).code, 'TRUNCATED', `length ${i}: ${(err as Error).message}`);
      truncations++;
    }
  }
  assert.ok(truncations > bytes.length * 0.8, `expected most cuts to be unsafe, got ${truncations}/${bytes.length}`);
  assert.ok(cleanCuts > 0, 'expected some cuts to land on field boundaries');
  assert.ok(decodeMessage(Person, bytes));
});

test('mid-field truncation always raises TRUNCATED with path', () => {
  const bytes = encodeMessage(Person, samplePerson);
  // find the offset of the "name" field payload (tag 0x0a, len, then bytes) and cut inside it
  const nameLen = bytes[1];
  assert.ok(nameLen >= 2, 'fixture name should be at least 2 bytes');
  for (const cut of [1, nameLen - 1]) {
    assert.throws(() => decodeMessage(Person, bytes.subarray(0, 2 + cut)), (e: unknown) => {
      assert.ok(e instanceof WireError);
      assert.equal((e as WireError).code, 'TRUNCATED');
      return true;
    });
  }
});

test('truncation error reports field path and offset', () => {
  const bytes = encodeMessage(Person, samplePerson);
  try {
    decodeMessage(Person, bytes.subarray(0, bytes.length - 1));
    assert.fail('should throw');
  } catch (err) {
    assert.ok(err instanceof WireError);
    assert.match((err as WireError).path, /^Person\./);
    assert.ok((err as WireError).offset >= 0);
  }
});

test('wire type mismatch is rejected', () => {
  const w = new WireWriter();
  w.writeTag(1, 0); w.writeVarint(1); // name as varint instead of LEN
  assert.throws(() => decodeMessage(Person, w.finish()), (e: unknown) => {
    assert.equal((e as WireError).code, 'WIRE_TYPE_MISMATCH');
    assert.equal((e as WireError).path, 'Person.name');
    return true;
  });
});

test('unknown fields are skipped by default and rejected on demand', () => {
  const w = new WireWriter();
  w.writeTag(1, 2); w.writeString('Bo');
  w.writeTag(99, 0); w.writeVarint(123);        // unknown varint
  w.writeTag(100, 2); w.writeLengthDelimited(new Uint8Array([9, 9])); // unknown len
  w.writeTag(101, 5); w.writeFixed32(1);        // unknown fixed32
  w.writeTag(102, 1); w.writeFixed64(2n);       // unknown fixed64
  const decoded = decodeMessage(Person, w.finish());
  assert.deepEqual(decoded, { name: 'Bo' });
  assert.throws(
    () => decodeMessage(Person, w.finish(), { unknownFields: 'error' }),
    (e: unknown) => (e as WireError).code === 'UNKNOWN_FIELD',
  );
});

test('truncated unknown field is still a safe bounds error', () => {
  const w = new WireWriter();
  w.writeTag(50, 2); w.writeVarint(10); w.writeBytes(new Uint8Array([1]));
  assert.throws(() => decodeMessage(Person, w.finish()), (e: unknown) => {
    assert.equal((e as WireError).code, 'TRUNCATED');
    assert.match((e as WireError).path, /#50/);
    return true;
  });
});

test('recursion limit guards against hostile nesting', () => {
  const Node: MessageDescriptor = {
    name: 'Node',
    fields: [],
  };
  Node.fields = [
    { no: 1, name: 'value', type: 'int32' },
    { no: 2, name: 'child', type: 'message', message: Node },
  ];
  let payload = new Uint8Array(0);
  for (let i = 0; i < 200; i++) {
    const w = new WireWriter();
    w.writeTag(2, 2);
    w.writeLengthDelimited(payload);
    payload = w.finish();
  }
  assert.throws(() => decodeMessage(Node, payload), (e: unknown) => {
    assert.equal((e as WireError).code, 'RECURSION_LIMIT');
    return true;
  });
  // with a higher limit the same payload decodes
  const decoded = decodeMessage(Node, payload, { maxDepth: 500 }) as { child: unknown };
  assert.ok(decoded.child);
});

test('invalid tag and invalid wire type are rejected', () => {
  assert.throws(() => decodeMessage(Person, new Uint8Array([0x00])), (e: unknown) => (e as WireError).code === 'INVALID_TAG');
  assert.throws(() => decodeMessage(Person, new Uint8Array([0x0f])), (e: unknown) => (e as WireError).code === 'INVALID_WIRE_TYPE');
  assert.throws(() => decodeMessage(Person, new Uint8Array([0x0c])), (e: unknown) => (e as WireError).code === 'INVALID_WIRE_TYPE'); // stray EGROUP
});

test('packed fixed32 with non-multiple length is a safe error', () => {
  const w = new WireWriter();
  w.writeTag(10, 2);
  w.writeLengthDelimited(new Uint8Array([1, 2, 3])); // 3 bytes, not multiple of 4
  assert.throws(() => decodeMessage(Person, w.finish()), (e: unknown) => (e as WireError).code === 'TRUNCATED');
});

test('invalid UTF-8 in nested string is reported with path', () => {
  const addr = new WireWriter();
  addr.writeTag(1, 2); addr.writeLengthDelimited(new Uint8Array([0xff]));
  const w = new WireWriter();
  w.writeTag(4, 2); w.writeLengthDelimited(addr.finish());
  assert.throws(() => decodeMessage(Person, w.finish()), (e: unknown) => {
    assert.equal((e as WireError).code, 'INVALID_UTF8');
    assert.match((e as WireError).path, /Person\.address/);
    return true;
  });
});

test('empty buffer decodes to empty object; empty message fields work', () => {
  assert.deepEqual(decodeMessage(Person, new Uint8Array(0)), {});
  const w = new WireWriter();
  w.writeTag(4, 2); w.writeLengthDelimited(new Uint8Array(0)); // empty Address
  assert.deepEqual(decodeMessage(Person, w.finish()), { address: {} });
});

test('decode does not mutate the input buffer', () => {
  const bytes = encodeMessage(Person, samplePerson);
  const snapshot = new Uint8Array(bytes);
  decodeMessage(Person, bytes);
  assert.deepEqual(bytes, snapshot);
});

test('decoder result has no reference to shared state across calls', () => {
  const bytes = encodeMessage(Person, samplePerson);
  const a = decodeMessage(Person, bytes) as typeof samplePerson;
  const b = decodeMessage(Person, bytes) as typeof samplePerson;
  assert.notEqual(a.scores, b.scores);
  (a.scores as number[]).push(999);
  assert.deepEqual(b.scores, samplePerson.scores);
});

test('Address schema decodes standalone', () => {
  const bytes = encodeMessage(Address, { city: '杭州', zip: 310000 });
  assert.deepEqual(decodeMessage(Address, bytes), { city: '杭州', zip: 310000 });
});
