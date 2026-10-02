import { test } from 'node:test';
import assert from 'node:assert/strict';
import { InstrumentedDecoder } from '../src/instrumented.ts';
import { MetricRegistry } from '../src/metrics.ts';
import type { MessageSchema } from '../src/decoder.ts';
import { ErrorCode, WireType, WireWriter } from '../src/wire.ts';

const schema: MessageSchema = {
  name: 'Event',
  fields: {
    1: { name: 'id', type: 'uint64' },
    2: { name: 'kind', type: 'string' },
    3: { name: 'deltas', type: 'sint64', repeated: true, packed: true },
  },
};

function goodPayload(): Uint8Array {
  const packed = new WireWriter().zigZag64(-5n).zigZag64(5n);
  return new WireWriter()
    .tag(1, WireType.VARINT).varint(123n)
    .tag(2, WireType.LEN).string('click')
    .tag(3, WireType.LEN).bytes(packed.finish())
    .tag(50, WireType.VARINT).varint(1) // unknown -> miss
    .finish();
}

test('instrumented decode records latency, totals, hit rate and depth', () => {
  const dec = new InstrumentedDecoder();
  const { value, stats } = dec.decode(schema, goodPayload());
  assert.equal(value.id, 123n);
  assert.deepEqual(value.deltas, [-5n, 5n]);
  assert.equal(stats.unknownFields, 1);

  const snap = dec.registry.snapshot();
  assert.equal(snap.counters['decode_total{message=Event}'], 1);
  assert.equal(snap.histograms['decode_latency_ms{message=Event}'].count, 1);
  const hr = snap.hitRates['field_hit_rate{message=Event}'];
  assert.equal(hr.hits, 3);
  assert.equal(hr.misses, 1);
  assert.ok(Math.abs(hr.rate - 0.75) < 1e-9);
});

test('instrumented decode tallies error codes on truncation', () => {
  const dec = new InstrumentedDecoder();
  const bad = new WireWriter().tag(1, WireType.VARINT).byte(0xff).finish();
  for (let i = 0; i < 3; i++) {
    assert.throws(() => dec.decode(schema, bad));
  }
  const snap = dec.registry.snapshot();
  assert.equal(snap.counters['decode_error_total{message=Event}'], 3);
  assert.equal(snap.errorCodes[ErrorCode.TRUNCATED], 3);
});

test('concurrent decodes keep metrics consistent and leak-free', async () => {
  const registry = new MetricRegistry();
  const dec = new InstrumentedDecoder(registry);
  const payload = goodPayload();
  const N = 200;
  await Promise.all(
    Array.from({ length: N }, async (_, i) => {
      if (i % 3 === 0) await Promise.resolve();
      dec.decode(schema, payload);
    }),
  );
  const snap = registry.snapshot();
  assert.equal(snap.counters['decode_total{message=Event}'], N);
  assert.equal(snap.histograms['decode_latency_ms{message=Event}'].count, N);
  assert.equal(snap.hitRates['field_hit_rate{message=Event}'].hits, 3 * N);
  assert.equal(snap.hitRates['field_hit_rate{message=Event}'].misses, N);
  assert.equal(registry.seriesCount, 4); // latency + total + hitrate + depth gauge
});
