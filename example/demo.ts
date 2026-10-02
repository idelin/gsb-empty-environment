/**
 * Demo: hand-encode a nested protobuf stream, decode it via the reflection
 * engine, trigger a truncation error, then export aggregated metrics.
 */
import { InstrumentedDecoder } from '../src/instrumented.ts';
import type { MessageSchema } from '../src/decoder.ts';
import { WireType, WireWriter } from '../src/wire.ts';

const orderSchema: MessageSchema = {
  name: 'Order',
  fields: {
    1: { name: 'orderId', type: 'uint64' },
    2: { name: 'item', type: 'string' },
    3: { name: 'priceCents', type: 'sint32' },
    4: { name: 'quantities', type: 'uint32', repeated: true, packed: true },
    5: {
      name: 'customer',
      type: 'message',
      schema: {
        name: 'Customer',
        fields: {
          1: { name: 'name', type: 'string' },
          2: { name: 'vip', type: 'bool' },
        },
      },
    },
  },
};

// 1. Hand-encode a binary stream (nested message + packed repeated).
const customer = new WireWriter().tag(1, WireType.LEN).string('Ada').tag(2, WireType.VARINT).varint(1);
const packedQty = new WireWriter().varint(3).varint(1).varint(4);
const stream = new WireWriter()
  .tag(1, WireType.VARINT).varint(20261002n)
  .tag(2, WireType.LEN).string('mechanical-keyboard')
  .tag(3, WireType.VARINT).zigZag32(-1599)
  .tag(4, WireType.LEN).bytes(packedQty.finish())
  .message(5, customer)
  .finish();

console.log('encoded bytes:', Buffer.from(stream).toString('hex'));

// 2. Decode with metrics instrumentation.
const decoder = new InstrumentedDecoder();
const { value, stats } = decoder.decode(orderSchema, stream);
console.log('decoded struct:', value);
console.log('decode stats:', stats);

// 3. Truncated stream -> safe bounds error, recorded as a metric.
const truncated = stream.slice(0, stream.length - 3);
try {
  decoder.decode(orderSchema, truncated);
} catch (err) {
  console.log('truncation safely rejected:', (err as Error).message);
}

// 4. Aggregated export.
console.log('\n--- metrics (JSON) ---');
console.log(JSON.stringify(decoder.registry.snapshot(), null, 2));
console.log('--- metrics (text) ---');
console.log(decoder.registry.exportText());
