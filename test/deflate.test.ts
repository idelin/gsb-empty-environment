import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import zlib from "node:zlib";
import {
  deflate,
  inflate,
  BLOCK_STORED,
  BLOCK_FIXED,
  BLOCK_DYNAMIC,
} from "../src/index.ts";
import { buildCodeLengths } from "../src/huffman.ts";

function sha256(data: Uint8Array): string {
  return createHash("sha256").update(data).digest("hex");
}

/** Synthetic system log with heavy repetition, ~200 KiB. */
function makeSystemLog(lines = 4000): Uint8Array {
  const hosts = ["web-01", "web-02", "db-01", "cache-01"];
  const levels = ["INFO", "WARN", "ERROR", "DEBUG"];
  const msgs = [
    "connection accepted from 10.0.0.12:54432",
    "query executed in 12ms: SELECT * FROM sessions WHERE id=?",
    "cache hit ratio 0.97, evictions 0",
    "retrying upstream api.internal:8080 (attempt 2/3)",
    "GC pause 23ms, heap 512MiB/1024MiB",
    "tls handshake completed: TLS_AES_256_GCM_SHA384",
  ];
  const parts: string[] = [];
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff);
  for (let i = 0; i < lines; i++) {
    const ts = new Date(1700000000000 + i * 137).toISOString();
    parts.push(
      `${ts} ${hosts[rand() % hosts.length]} app[${1000 + (rand() % 9)}]: ` +
        `${levels[rand() % levels.length]} request_id=${(rand() % 1e6)
          .toString(16)
          .padStart(6, "0")} ${msgs[rand() % msgs.length]}\n`,
    );
  }
  return new TextEncoder().encode(parts.join(""));
}

const fixtures: Record<string, Uint8Array> = {
  empty: new Uint8Array(0),
  oneByte: new Uint8Array([0x41]),
  threeBytes: new TextEncoder().encode("abc"),
  sameByte64K: new Uint8Array(65536).fill(0x61),
  allByteValues: Uint8Array.from({ length: 256 }, (_, i) => i),
  random4K: new Uint8Array(randomBytes(4096)),
  random200K: new Uint8Array(randomBytes(200 * 1024)),
  systemLog: makeSystemLog(),
  windowEdgeRepeat: (() => {
    // Pattern repeated at a distance slightly beyond 32 KiB plus one exactly at it.
    const chunk = new Uint8Array(randomBytes(1024));
    const filler = new Uint8Array(randomBytes(32768 - 1024));
    const out = new Uint8Array(1024 + 32768 + 1024 + 1024);
    out.set(chunk, 0);
    out.set(filler, 1024);
    out.set(chunk, 1024 + 32768); // distance exactly 32768
    out.set(chunk, 1024 + 32768 + 1024); // distance 32768 again
    return out;
  })(),
};

test("round-trip: deflate -> inflate restores SHA256 for every fixture", () => {
  for (const [name, original] of Object.entries(fixtures)) {
    const { data: compressed } = deflate(original);
    const restored = inflate(compressed);
    assert.equal(
      sha256(restored),
      sha256(original),
      `fixture ${name}: SHA256 mismatch after round-trip`,
    );
  }
});

test("encoder output is accepted by the reference zlib inflater", () => {
  for (const [name, original] of Object.entries(fixtures)) {
    const { data: compressed } = deflate(original);
    const restored = zlib.inflateRawSync(compressed);
    assert.equal(sha256(restored), sha256(original), `fixture ${name}`);
  }
});

test("inflate handles dynamic-huffman blocks produced by zlib", () => {
  for (const [name, original] of Object.entries(fixtures)) {
    const compressed = zlib.deflateRawSync(original, { level: 9 });
    const restored = inflate(new Uint8Array(compressed));
    assert.equal(sha256(restored), sha256(original), `fixture ${name}`);
  }
});

test("inflate handles fixed-huffman (static) blocks produced by zlib", () => {
  for (const [name, original] of Object.entries(fixtures)) {
    const compressed = zlib.deflateRawSync(original, {
      level: 9,
      strategy: zlib.constants.Z_FIXED,
    });
    const restored = inflate(new Uint8Array(compressed));
    assert.equal(sha256(restored), sha256(original), `fixture ${name}`);
  }
});

test("inflate handles stored (uncompressed) blocks produced by zlib", () => {
  for (const [name, original] of Object.entries(fixtures)) {
    const compressed = zlib.deflateRawSync(original, { level: 0 });
    const restored = inflate(new Uint8Array(compressed));
    assert.equal(sha256(restored), sha256(original), `fixture ${name}`);
  }
});

test("block-type selection: repetitive log -> dynamic, random -> stored", () => {
  assert.equal(deflate(fixtures.systemLog).blockType, BLOCK_DYNAMIC);
  assert.equal(deflate(fixtures.random4K).blockType, BLOCK_STORED);
  // Empty input: a bare fixed-table end-of-block code (2 bytes) beats a
  // 5-byte stored block, matching what zlib emits for empty input.
  assert.equal(deflate(fixtures.empty).blockType, BLOCK_FIXED);
});

test("fixed block is emitted when it is the smallest encoding", () => {
  // Short, mildly repetitive text: fixed table beats dynamic header overhead.
  const data = new TextEncoder().encode("the quick brown fox jumps over the lazy dog. ".repeat(8));
  const { blockType } = deflate(data);
  assert.equal(blockType, BLOCK_FIXED);
  assert.equal(sha256(inflate(deflate(data).data)), sha256(data));
});

test("repetitive log compresses well below original size", () => {
  const original = fixtures.systemLog;
  const { data: compressed } = deflate(original);
  assert.ok(
    compressed.length < original.length * 0.35,
    `ratio ${(compressed.length / original.length).toFixed(3)} too high`,
  );
});

test("huffman code lengths respect the bit limit and satisfy Kraft equality", () => {
  const freqs = new Array<number>(286).fill(0);
  let seed = 7;
  for (let i = 0; i < freqs.length; i++) {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    freqs[i] = seed % 1000;
  }
  const lengths = buildCodeLengths(freqs, 15);
  let kraft = 0;
  for (const len of lengths) {
    assert.ok(len <= 15, `length ${len} exceeds limit`);
    if (len > 0) kraft += 2 ** -len;
  }
  assert.ok(Math.abs(kraft - 1) < 1e-9, `Kraft sum ${kraft} != 1`);
});

test("inflate rejects corrupted streams", () => {
  const { data: compressed } = deflate(fixtures.systemLog);
  // Truncated stream: decoder must run out of input.
  assert.throws(() => inflate(compressed.slice(0, compressed.length >> 1)));
  // Reserved block type 3 in the first block header.
  const badType = compressed.slice();
  badType[0] = (badType[0] & ~0x06) | 0x06;
  assert.throws(() => inflate(badType));
  assert.throws(() => inflate(new Uint8Array([0x07]))); // bfinal + btype 3
});
