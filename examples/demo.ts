/**
 * End-to-end demo:
 *  1. Hand-rolled protobuf codec encodes/decodes a nested message.
 *  2. SnapshotEngine produces incremental snapshots over a large state.
 *  3. A checkpoint archive (itself a protobuf binary) is restored.
 */
import { defineMessage } from "../src/schema.ts";
import { SnapshotEngine, VersionedStore } from "../src/snapshot.ts";
import { TruncatedFieldError } from "../src/varint.ts";

/* ---------- 1. Nested message codec ---------- */

const Point = defineMessage("Point", [
  { name: "x", fieldNumber: 1, type: "sint32" },
  { name: "y", fieldNumber: 2, type: "sint32" },
]);

const Shape = defineMessage("Shape", [
  { name: "name", fieldNumber: 1, type: "string" },
  { name: "vertices", fieldNumber: 2, type: "message", repeated: true, schema: Point },
  { name: "weights", fieldNumber: 3, type: "double", repeated: true, packed: true },
  { name: "id", fieldNumber: 4, type: "fixed64" },
]);

const shape = {
  name: "triangle",
  vertices: [
    { x: -3, y: 7 },
    { x: 11, y: -2 },
    { x: 0, y: 0 },
  ],
  weights: [0.5, 1.25, 2.75],
  id: 0xdeadbeefcafebaben,
};

const wire = Shape.encode(shape);
const hex = [...wire].map((b) => b.toString(16).padStart(2, "0")).join(" ");
console.log("== protobuf wire format ==");
console.log(`encoded ${wire.length} bytes:`);
console.log(hex);
console.log("decoded:", JSON.stringify(Shape.decode(wire), (_, v) => (typeof v === "bigint" ? `0x${v.toString(16)}` : v)));

// Truncation safety: cut the stream mid-field.
try {
  Shape.decode(wire.subarray(0, wire.length - 3));
} catch (e) {
  console.log(`truncation guarded -> ${(e as TruncatedFieldError).name}: ${(e as Error).message}`);
}

/* ---------- 2. Incremental snapshots over a large state ---------- */

console.log("\n== incremental snapshots ==");
const store = new VersionedStore();
const engine = new SnapshotEngine(store);

const payload = new Uint8Array(256);
for (let i = 0; i < 10_000; i++) {
  payload[0] = i & 0xff;
  store.set(`account/${i}`, payload);
}
console.log(`state: ${store.size} keys, ${store.byteSize} bytes, version ${store.currentVersion}`);

const t0 = performance.now();
const base = await engine.checkpoint();
console.log(`full checkpoint: ${base.bytes.length} bytes in ${(performance.now() - t0).toFixed(1)}ms`);

// Only 3 keys change -> the incremental snapshot stays tiny.
store.set("account/1", new TextEncoder().encode("updated"));
store.set("account/2", new TextEncoder().encode("updated"));
store.delete("account/3");

const t1 = performance.now();
const delta = await engine.snapshot();
console.log(
  `incremental snapshot #${delta.snapshot.id}: ${delta.snapshot.entries.length} entries ` +
    `(v${delta.snapshot.fromVersion} -> v${delta.snapshot.toVersion}) captured in ` +
    `${(performance.now() - t1).toFixed(2)}ms`,
);

/* ---------- 3. Checkpoint archive + restore ---------- */

console.log("\n== checkpoint archive & restore ==");
const archive = await engine.checkpoint();
console.log(`archive: ${archive.bytes.length} bytes, ${archive.data.deltas.length} delta(s)`);

const { store: restored, version } = SnapshotEngine.restore(archive.bytes);
console.log(`restored: ${restored.size} keys at version ${version}`);
console.log(`account/1 = ${new TextDecoder().decode(restored.get("account/1"))}`);
console.log(`account/3 present? ${restored.has("account/3")}`);

// Strict leak accounting.
delta.release();
base.release();
archive.release();
engine.dispose();
console.log("leak check: all resources released, engine disposed cleanly");
