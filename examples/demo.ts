import { ProtoCodec, SparseVector, packDeltas, unpackDeltas, rleCompress } from "../src/index.ts";
import type { MessageSchema } from "../src/index.ts";

/* 1. Define a nested schema with packed repeated fields. */
const Point: MessageSchema = {
  name: "Point",
  fields: [
    { no: 1, name: "x", type: "sint32", required: true },
    { no: 2, name: "y", type: "sint32", required: true },
  ],
};

const Track: MessageSchema = {
  name: "Track",
  fields: [
    { no: 1, name: "id", type: "uint64", required: true },
    { no: 2, name: "label", type: "string" },
    { no: 3, name: "points", type: "message", message: Point, repeated: true },
    { no: 4, name: "samples", type: "sint64", repeated: true, packed: true },
  ],
};

const codec = new ProtoCodec(Track);
const original = {
  id: 9_007_199_254_740_993n,
  label: "轨道-A 🛰",
  points: [
    { x: -3, y: 7 },
    { x: 1024, y: -2048 },
  ],
  samples: [0n, -1n, 1n, -1_000_000n, 1_000_000n],
};

const bytes = codec.encode(original);
console.log(`encoded ${bytes.length} bytes:`);
console.log("  " + [...bytes].map((b) => b.toString(16).padStart(2, "0")).join(" "));

const decoded = codec.decode(bytes);
console.log("decoded:", JSON.stringify(decoded, (_, v) => (typeof v === "bigint" ? `${v}n` : v)));
console.log("round-trip equal:", JSON.stringify(decoded, bigintKey) === JSON.stringify(original, bigintKey));

function bigintKey(_: string, v: unknown) {
  return typeof v === "bigint" ? `${v}n` : v;
}

/* 2. Truncation safety demo. */
try {
  codec.decode(bytes.subarray(0, bytes.length - 3));
} catch (err) {
  console.log("truncation safely reported:", (err as Error).message);
}

/* 3. Sparse-data memory optimizer demo. */
const vec = new SparseVector({ fill: 0 });
for (let i = 0; i < 1_000_000; i += 10_000) vec.set(i, i * 2);
let s = vec.stats();
console.log(
  `sparse: ${s.nonDefault} live of ${s.length} slots -> mode=${s.mode}, ~${s.currentBytes}B (dense would be ~${s.denseBytes}B)`,
);
for (let i = 0; i < 1_000_000; i++) vec.set(i, i + 1);
s = vec.stats();
console.log(`densified: mode=${s.mode}, ~${s.currentBytes}B (sparse would be ~${s.sparseBytes}B)`);
for (let i = 0; i < 1_000_000; i++) if (i % 10_000 !== 0) vec.delete(i);
s = vec.stats();
console.log(`sparsified again: mode=${s.mode}, ~${s.currentBytes}B`);

/* 4. Compression demo: 1M sparse indices -> tiny delta pack. */
const indices: number[] = [];
for (let i = 0; i < 1_000_000; i += 997) indices.push(i);
const packed = packDeltas(indices);
console.log(
  `delta-pack: ${indices.length} indices (${indices.length * 8}B raw) -> ${packed.length}B`,
);
console.log("delta-pack round-trip ok:", unpackDeltas(packed).length === indices.length);

const runs = rleCompress(new Array(100_000).fill(0).fill(1, 50_000, 50_005));
console.log(`rle: 100000 zeros with a spike -> ${runs.pairs.length / 2} run(s)`);
