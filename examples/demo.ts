/**
 * Demo: compress a synthetic, highly repetitive system log with the pure
 * stdlib Deflate pipeline, verify the round-trip with SHA256, and
 * cross-check the stream against Node's reference zlib.
 */
import { createHash } from "node:crypto";
import zlib from "node:zlib";
import { deflate, inflate } from "../src/index.ts";

const BLOCK_NAMES = ["stored", "fixed-huffman", "dynamic-huffman"] as const;

function makeSystemLog(lines: number): Uint8Array {
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

const sha256 = (d: Uint8Array) => createHash("sha256").update(d).digest("hex");

const original = makeSystemLog(4000);
console.log(`original      : ${original.length} bytes`);
console.log(`original sha256: ${sha256(original)}`);

const t0 = performance.now();
const { data: compressed, blockType } = deflate(original);
const t1 = performance.now();
console.log(
  `compressed    : ${compressed.length} bytes ` +
    `(ratio ${(compressed.length / original.length).toFixed(3)}, ` +
    `block=${BLOCK_NAMES[blockType]}, ${(t1 - t0).toFixed(1)} ms)`,
);

const restored = inflate(compressed);
console.log(`restored sha256: ${sha256(restored)}`);
console.log(`round-trip     : ${sha256(restored) === sha256(original) ? "OK (byte-identical)" : "MISMATCH"}`);

const viaZlib = zlib.inflateRawSync(compressed);
console.log(
  `zlib cross-check: ${sha256(new Uint8Array(viaZlib)) === sha256(original) ? "OK" : "MISMATCH"}`,
);
