import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import {
  rate,
  increase,
  irate,
  isStaleNaN,
  decodeSamples,
  decodeColumnarSamples,
  STALE_NAN_BITS,
} from './index.js';
import { s, staleNaN, ordinaryNaN, floatFromBits, encodeInterleaved, closeTo } from './test-helpers.js';

describe('StaleNaN bit-exact detection', () => {
  test('isStaleNaN distinguishes the marker from ordinary NaNs', () => {
    assert.equal(isStaleNaN(staleNaN()), true);
    assert.equal(isStaleNaN(ordinaryNaN()), false);
    assert.equal(isStaleNaN(floatFromBits(0x7ff8000000000001n)), false);
    assert.equal(isStaleNaN(1.5), false);
    assert.equal(STALE_NAN_BITS, 0x7ff0000000000002n);
  });

  test('StaleNaN samples are filtered and are not treated as resets', () => {
    // Valid points 100 -> 130 with a stale marker between them. Without the
    // filter a reset rule could misfire; the stale sample simply vanishes and
    // the two valid points remain adjacent (delta 30). Two points spanning the
    // full window give factor 1 (rate 1/s) and an increase duration of
    // range - avg = 0, so increase is 0.
    const samples = [s(0, 100), s(10000, staleNaN()), s(30000, 130)];
    assert.ok(closeTo(rate(samples, 30000, 30000) as number, 1));
    assert.ok(closeTo(increase(samples, 30000, 30000) as number, 0));
  });

  test('all-stale or single-valid windows produce undefined', () => {
    assert.equal(rate([s(0, staleNaN()), s(10000, staleNaN())], 30000, 30000), undefined);
    assert.equal(irate([s(0, 1), s(10000, staleNaN())], 30000, 30000), undefined);
  });

  test('ordinary NaN propagates while an adjacent stale marker is dropped', () => {
    // stale -> dropped; ordinary NaN -> survives filtering and forces NaN out.
    const samples = [s(0, 100), s(10000, staleNaN()), s(20000, ordinaryNaN()), s(30000, 130)];
    assert.ok(Number.isNaN(rate(samples, 30000, 30000) as number));
    assert.ok(Number.isNaN(increase(samples, 30000, 30000) as number));
  });

  test('ordinary NaN alone propagates as NaN, not undefined', () => {
    const samples = [s(0, 5), s(15000, ordinaryNaN()), s(30000, 6)];
    const result = rate(samples, 30000, 30000);
    assert.notEqual(result, undefined);
    assert.ok(Number.isNaN(result as number));
  });
});

describe('binary decoding preserves the stale bit pattern', () => {
  test('decodeSamples reads raw bits via DataView and flags the marker', () => {
    const buffer = encodeInterleaved([
      [0, 0x4059000000000000n], // 100.0
      [10000, STALE_NAN_BITS], // stale marker
      [20000, 0x7ff8000000000000n], // ordinary canonical NaN
      [30000, 0x4060400000000000n], // 130.0
    ]);
    const decoded = decodeSamples(buffer, { count: 4 });
    assert.equal(decoded[0].v, 100);
    assert.equal(decoded[1].stale, true);
    assert.equal(decoded[2].stale, false);
    assert.ok(Number.isNaN(decoded[2].v));
    assert.equal(decoded[3].v, 130);

    // Stale point removed, ordinary NaN kept and propagated => NaN result.
    assert.ok(Number.isNaN(rate(decoded, 30000, 30000) as number));
  });

  test('buffer containing only stale markers plus one value yields undefined', () => {
    const buffer = encodeInterleaved([
      [0, STALE_NAN_BITS],
      [10000, STALE_NAN_BITS],
      [20000, 0x3ff0000000000000n], // 1.0
    ]);
    const decoded = decodeSamples(buffer, { count: 3 });
    assert.equal(rate(decoded, 30000, 30000), undefined);
  });

  test('decodeColumnarSamples distinguishes stale from ordinary NaN', () => {
    const timestampBuffer = new ArrayBuffer(24);
    const timestamps = new BigInt64Array(timestampBuffer);
    timestamps[0] = 0n;
    timestamps[1] = 10000n;
    timestamps[2] = 20000n;
    const valueBuffer = new ArrayBuffer(24);
    const valueView = new DataView(valueBuffer);
    valueView.setBigUint64(0, 0x3ff0000000000000n, true); // 1
    valueView.setBigUint64(8, STALE_NAN_BITS, true);
    valueView.setBigUint64(16, 0x4000000000000000n, true); // 2
    const decoded = decodeColumnarSamples(timestamps, valueBuffer);
    assert.equal(decoded[1].stale, true);
    assert.ok(closeTo(rate(decoded, 20000, 20000) as number, 0.05));
  });
});
