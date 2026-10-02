/**
 * Demo: hand-crafted multi-level nested protobuf message with packed
 * repeated arrays, encoded and decoded with zero external dependencies.
 *
 *   message Inner  { sint32 delta = 1; fixed64 stamp = 2; }
 *   message Middle { string label = 1; repeated sint32 scores = 2 [packed];
 *                    Inner inner = 3; }
 *   message Outer  { uint32 id = 1; Middle mid = 2;
 *                    repeated fixed32 ratios = 3 [packed]; bytes blob = 4; }
 *
 * Run: node examples/demo.ts
 */
import { WireEngine } from "../src/engine.ts";
import { TruncatedFieldError } from "../src/errors.ts";
import type { MessageSchema } from "../src/schema.ts";
import { WireWriter } from "../src/writer.ts";

const Inner: MessageSchema = {
  name: "Inner",
  fields: [
    { field: 1, name: "delta", kind: "zigzag32" },
    { field: 2, name: "stamp", kind: "fixed64" },
  ],
};

const Middle: MessageSchema = {
  name: "Middle",
  fields: [
    { field: 1, name: "label", kind: "string" },
    { field: 2, name: "scores", kind: "packed-zigzag32" },
    { field: 3, name: "inner", kind: "message", schema: Inner },
  ],
};

const Outer: MessageSchema = {
  name: "Outer",
  fields: [
    { field: 1, name: "id", kind: "varint" },
    { field: 2, name: "mid", kind: "message", schema: Middle },
    { field: 3, name: "ratios", kind: "packed-fixed32" },
    { field: 4, name: "blob", kind: "bytes" },
  ],
};

const engine = new WireEngine(64);

// ---- encode (pooled writer) ----
const bytes = engine.withWriter((w: WireWriter) => {
  w.fieldVarint(1, 9001);

  const mid = new WireWriter();
  mid.fieldString(1, "nested-label");
  const packed = new WireWriter();
  for (const s of [-5, 0, 7, 123456, -987654]) packed.writeZigZag32(s);
  mid.fieldBytes(2, packed.view());
  const inner = new WireWriter();
  inner.fieldZigZag32(1, -42);
  inner.fieldFixed64(2, 0xdeadbeefcafe1234n);
  mid.fieldBytes(3, inner.view());
  w.fieldBytes(2, mid.view());

  const ratios = new WireWriter();
  for (const r of [1, 65536, 4294967295]) ratios.writeFixed32(r);
  w.fieldBytes(3, ratios.view());

  w.fieldBytes(4, new Uint8Array([0xde, 0xad, 0xbe, 0xef]));
  return w.finish();
});

console.log(`encoded ${bytes.length} bytes:`,
  Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join(" "));

// ---- decode (pooled reader, fresh target) ----
const decoded = engine.decode(bytes, Outer);
console.log("\ndecoded:", JSON.stringify(decoded, (_k, v) =>
  typeof v === "bigint" ? `0x${v.toString(16)}` : v));

// ---- zero-allocation steady state: reuse one target graph ----
const warmTarget = engine.decode(bytes, Outer);
const readerStatsBefore = engine.readerPool.stats();
for (let i = 0; i < 100_000; i++) engine.decode(bytes, Outer, warmTarget);
const readerStatsAfter = engine.readerPool.stats();
console.log(
  `\n100k pooled decodes: readers created ${readerStatsBefore.created} -> ` +
    `${readerStatsAfter.created} (reused ${readerStatsAfter.reused}x), ` +
    `leak-free: ${engine.leakFree}`,
);

// ---- decode cache ----
const c1 = engine.decodeCached(7, bytes, Outer);
const c2 = engine.decodeCached(7, bytes, Outer);
console.log("cache hit returns identical object:", c1 === c2,
  JSON.stringify(engine.cache.stats()));

// ---- truncation safety ----
const cut = bytes.subarray(0, bytes.length - 3);
try {
  engine.decode(cut, Outer);
  console.log("ERROR: truncation not detected");
} catch (e) {
  if (e instanceof TruncatedFieldError) {
    console.log(`\ntruncation guarded: ${e.message}`);
  } else {
    throw e;
  }
}
