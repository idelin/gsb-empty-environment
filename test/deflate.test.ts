import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash, randomBytes } from 'node:crypto';
import zlib from 'node:zlib';
import { deflate, inflate } from '../src/index.ts';
import { generateSystemLog } from '../examples/syslog.ts';

function sha256(data: Buffer): string {
  return createHash('sha256').update(data).digest('hex');
}

function assertRoundTrip(data: Buffer, label: string): Buffer {
  const compressed = deflate(data);
  const restored = inflate(compressed);
  assert.equal(
    sha256(restored),
    sha256(data),
    `${label}: SHA256 mismatch after round trip`,
  );
  assert.ok(restored.equals(data), `${label}: byte mismatch after round trip`);
  return compressed;
}

test('system log with heavy repetition: round trip + SHA256 match', () => {
  const log = generateSystemLog(512 * 1024);
  const compressed = assertRoundTrip(log, 'system log');
  const ratio = (compressed.length / log.length * 100).toFixed(1);
  console.log(`  log ${log.length} B -> ${compressed.length} B (${ratio}%)`);
  assert.ok(compressed.length < log.length * 0.3, 'expected strong compression on repetitive logs');
});

test('our dynamic/static output is decodable by node:zlib (inflateRaw)', () => {
  const log = generateSystemLog(128 * 1024, 7);
  const restored = zlib.inflateRawSync(deflate(log));
  assert.equal(sha256(restored), sha256(log));
});

test('inflate handles dynamic Huffman blocks produced by zlib', () => {
  const log = generateSystemLog(64 * 1024, 11);
  const compressed = zlib.deflateRawSync(log, { level: 9 });
  assert.equal(compressed[0] & 6, 4, 'expected a dynamic block from zlib'); // BTYPE=10
  const restored = inflate(compressed);
  assert.equal(sha256(restored), sha256(log));
});

test('inflate handles static (fixed) Huffman blocks produced by zlib', () => {
  const log = generateSystemLog(8 * 1024, 13);
  const compressed = zlib.deflateRawSync(log, {
    level: 9,
    strategy: zlib.constants.Z_FIXED,
  });
  assert.equal(compressed[0] & 6, 2, 'expected a fixed block from zlib'); // BTYPE=01
  const restored = inflate(compressed);
  assert.equal(sha256(restored), sha256(log));
});

test('inflate handles stored (non-compressed) blocks produced by zlib', () => {
  const data = randomBytes(100 * 1024);
  const compressed = zlib.deflateRawSync(data, { level: 0 });
  assert.equal(compressed[0] & 6, 0, 'expected a stored block from zlib'); // BTYPE=00
  const restored = inflate(compressed);
  assert.equal(sha256(restored), sha256(data));
});

test('incompressible data falls back to stored blocks and round trips', () => {
  const data = randomBytes(200 * 1024); // > 65535: multiple stored chunks
  const compressed = deflate(data);
  assert.equal(compressed[0] & 6, 0, 'expected stored block selection');
  assert.ok(compressed.length < data.length + 1024, 'stored overhead must be tiny');
  const restored = inflate(compressed);
  assert.equal(sha256(restored), sha256(data));
  assert.ok(zlib.inflateRawSync(compressed).equals(data), 'zlib must decode our stored blocks');
});

test('edge cases: empty, single byte, uniform run, tiny text', () => {
  for (const [label, data] of [
    ['empty', Buffer.alloc(0)],
    ['one byte', Buffer.from([0x41])],
    ['uniform run', Buffer.alloc(100_000, 0x7a)],
    ['tiny text', Buffer.from('hello hello hello hello')],
  ] as const) {
    const compressed = assertRoundTrip(data, label);
    assert.ok(zlib.inflateRawSync(compressed).equals(data), `zlib decodes our ${label} output`);
  }
});

test('matches spanning the 32 KiB window boundary round trip', () => {
  const chunk = randomBytes(4096);
  const filler = randomBytes(32768);
  // Repeat `chunk` just inside and just outside the window limit.
  const data = Buffer.concat([chunk, filler, chunk, filler.subarray(0, 1000), chunk]);
  assertRoundTrip(data, 'window boundary');
});

test('all byte values and long literal runs round trip', () => {
  const parts: Buffer[] = [];
  for (let i = 0; i < 256; i++) parts.push(Buffer.alloc(64, i));
  parts.push(randomBytes(50_000));
  for (let i = 255; i >= 0; i--) parts.push(Buffer.alloc(64, i));
  assertRoundTrip(Buffer.concat(parts), 'all byte values');
});

test('rejects corrupt streams', () => {
  const log = generateSystemLog(4096, 3);
  const compressed = deflate(log);
  const corrupt = Buffer.from(compressed);
  corrupt[corrupt.length - 2] ^= 0xff;
  assert.throws(() => inflate(corrupt));
  assert.throws(() => inflate(Buffer.from([0x07])), 'reserved BTYPE must fail');
});
