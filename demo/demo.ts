import { BufferWriter, ProtoEngine, WireType } from '../src/index.ts';
import type { MessageDescriptor } from '../src/index.ts';

// ---- Schema (reflection descriptors) --------------------------------------
const Address: MessageDescriptor = {
  name: 'Address',
  fields: [
    { number: 1, name: 'city', type: 'string', required: true },
    { number: 2, name: 'zip', type: 'string' },
  ],
};

const User: MessageDescriptor = {
  name: 'User',
  fields: [
    { number: 1, name: 'id', type: 'uint64', required: true },
    { number: 2, name: 'name', type: 'string', required: true },
    { number: 3, name: 'balance', type: 'sint64' },
    { number: 4, name: 'scores', type: 'int32', repeated: true, packed: true },
    { number: 5, name: 'address', type: 'message', message: Address },
    {
      number: 6, name: 'email', type: 'string',
      validate: (v) => (typeof v === 'string' && v.includes('@') ? undefined : 'email must contain @'),
    },
    { number: 7, name: 'ratio', type: 'double' },
  ],
};

// ---- Encode a multi-level nested message by hand --------------------------
function encodeUser(): Uint8Array {
  const addr = new BufferWriter()
    .writeTag(1, WireType.LengthDelimited).writeString('Shanghai')
    .writeTag(2, WireType.LengthDelimited).writeString('200000')
    .finish();

  const packed = new BufferWriter();
  for (const s of [7, -3, 42, 1024]) packed.writeVarint(s >>> 0);

  return new BufferWriter()
    .writeTag(1, WireType.Varint).writeVarint(9007199254740993n) // > 2^53
    .writeTag(2, WireType.LengthDelimited).writeString('ada')
    .writeTag(3, WireType.Varint).writeZigZag64(-123456789012345n)
    .writeTag(4, WireType.LengthDelimited).writeBytes(packed.finish()) // packed repeated
    .writeTag(5, WireType.LengthDelimited).writeBytes(addr) // nested message
    .writeTag(6, WireType.LengthDelimited).writeString('ada@example.com')
    .writeTag(7, WireType.Fixed64).writeDouble(0.875)
    .finish();
}

const engine = new ProtoEngine();

const bytes = encodeUser();
console.log(`encoded ${bytes.byteLength} bytes:`, Buffer.from(bytes).toString('hex'));

const outcome = engine.decode(User, bytes);
console.log('\ndecoded:', JSON.stringify(outcome.value, (_, v) => (typeof v === 'bigint' ? v.toString() : v), 2));
console.log('validation valid:', outcome.validation?.valid);

// Truncation: cut the stream mid-field and confirm safe OOB reporting.
const truncated = bytes.subarray(0, bytes.byteLength - 3);
const bad = engine.decode(User, truncated);
console.log('\ntruncated decode ok?', bad.ok, '->', bad.error?.message);

// Validation failure path.
const invalidBytes = new BufferWriter()
  .writeTag(1, WireType.Varint).writeVarint(1n)
  .writeTag(6, WireType.LengthDelimited).writeString('not-an-email')
  .finish();
const invalid = engine.decode(User, invalidBytes);
console.log('invalid message issues:', invalid.validation?.issues);

console.log('\n--- metrics (JSON aggregation) ---');
console.log(JSON.stringify(engine.exportMetrics(), null, 2));
console.log('--- metrics (text exposition) ---');
process.stdout.write(engine.exportMetricsText());
