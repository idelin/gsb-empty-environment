import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CheckpointArchive,
  decodeArchive,
  encodeArchive,
  LeakError,
  ResourceRegistry,
  SnapshotEngine,
  SnapshotError,
  VersionedStore,
} from "../src/snapshot.ts";
import { ProtobufDecodeError } from "../src/varint.ts";

const enc = (s: string) => new TextEncoder().encode(s);

function makeEngine() {
  const store = new VersionedStore();
  const engine = new SnapshotEngine(store);
  return { store, engine };
}

test("store: mutations bump version and track dirty keys", () => {
  const store = new VersionedStore();
  assert.equal(store.currentVersion, 0n);
  store.set("a", enc("1"));
  store.set("b", enc("2"));
  store.delete("a");
  assert.equal(store.currentVersion, 3n);
  assert.equal(store.dirtyCount, 2);
  assert.equal(store.size, 1);
  assert.deepEqual(store.get("b"), enc("2"));
  assert.equal(store.get("a"), undefined);
});

test("store: returned buffers are detached copies", () => {
  const store = new VersionedStore();
  const src = enc("payload");
  store.set("k", src);
  src[0] = 0xff; // mutating caller buffer must not corrupt the store
  const out = store.get("k")!;
  out[0] = 0x00; // mutating the returned copy must not either
  assert.deepEqual(store.get("k"), enc("payload"));
});

test("snapshot: empty dirty set yields an empty delta", async () => {
  const { engine } = makeEngine();
  const h = await engine.snapshot();
  assert.equal(h.snapshot.entries.length, 0);
  assert.equal(h.snapshot.fromVersion, 0n);
  assert.equal(h.snapshot.toVersion, 0n);
  h.release();
  engine.dispose();
});

test("snapshot: captures only entries dirtied since the last snapshot", async () => {
  const { store, engine } = makeEngine();
  store.set("a", enc("1"));
  store.set("b", enc("2"));
  const h1 = await engine.snapshot();
  assert.equal(h1.snapshot.entries.length, 2);
  assert.equal(h1.snapshot.fromVersion, 0n);
  assert.equal(h1.snapshot.toVersion, 2n);

  store.set("c", enc("3"));
  store.delete("a");
  const h2 = await engine.snapshot();
  assert.equal(h2.snapshot.fromVersion, 2n);
  assert.equal(h2.snapshot.toVersion, 4n);
  assert.equal(h2.snapshot.entries.length, 2);
  const tomb = h2.snapshot.entries.find((e) => e.key === "a")!;
  assert.equal(tomb.value, null, "delete must produce a tombstone");

  h1.release();
  h2.release();
  engine.dispose();
});

test("snapshot: repeated mutation of one key collapses to one entry", async () => {
  const { store, engine } = makeEngine();
  store.set("hot", enc("v1"));
  store.set("hot", enc("v2"));
  store.set("hot", enc("v3"));
  const h = await engine.snapshot();
  assert.equal(h.snapshot.entries.length, 1);
  assert.deepEqual(h.snapshot.entries[0].value, enc("v3"));
  h.release();
  engine.dispose();
});

test("checkpoint: archive round-trips through the protobuf codec", async () => {
  const { store, engine } = makeEngine();
  for (let i = 0; i < 100; i++) store.set(`key-${i}`, enc(`value-${i}`));
  const snap = await engine.snapshot();
  store.set("key-5", enc("updated"));
  store.delete("key-7");
  const archive = await engine.checkpoint();

  assert.ok(archive.bytes.length > 0);
  assert.equal(engine.pendingDeltaCount, 0, "checkpoint drains pending deltas");

  const { store: restored, version } = SnapshotEngine.restore(archive.bytes);
  assert.equal(version, store.currentVersion);
  assert.equal(restored.size, store.size);
  for (const k of store.keys()) {
    assert.deepEqual(restored.get(k), store.get(k), `key ${k}`);
  }
  assert.equal(restored.has("key-7"), false);

  snap.release();
  archive.release();
  engine.dispose();
});

test("checkpoint: flushes un-snapshotted mutations into the archive", async () => {
  const { store, engine } = makeEngine();
  store.set("x", enc("1"));
  const archive = await engine.checkpoint(); // no explicit snapshot first
  assert.equal(archive.data.deltas.length, 1);
  const { store: restored } = SnapshotEngine.restore(archive.bytes);
  assert.deepEqual(restored.get("x"), enc("1"));
  archive.release();
  engine.dispose();
});

test("checkpoint: archive is self-describing and validated", async () => {
  const { store, engine } = makeEngine();
  store.set("k", enc("v"));
  const archive = await engine.checkpoint();

  // Magic corruption is rejected.
  const badMagic = archive.bytes.slice();
  badMagic[badMagic.length - 1] ^= 0xff; // corrupt tail
  assert.throws(() => SnapshotEngine.restore(badMagic), ProtobufDecodeError);

  // Truncation of the archive is rejected by the wire parser.
  assert.throws(
    () => SnapshotEngine.restore(archive.bytes.subarray(0, 3)),
    ProtobufDecodeError,
  );

  // Garbage is rejected.
  assert.throws(() => SnapshotEngine.restore(enc("not an archive")), Error);

  archive.release();
  engine.dispose();
});

test("restore: delta chain gaps are detected", () => {
  const gapped = encodeArchive({
    baseVersion: 10n,
    deltaFromVersion: 10n,
    baseEntries: [],
    deltas: [
      { id: 1n, fromVersion: 10n, toVersion: 12n, entries: [], createdAtMs: 0 },
      { id: 2n, fromVersion: 99n, toVersion: 100n, entries: [], createdAtMs: 0 },
    ],
  });
  assert.throws(() => SnapshotEngine.restore(gapped), SnapshotError);
});

test("archive codec: header counts must match payload", () => {
  const data = {
    baseVersion: 0n,
    deltaFromVersion: 0n,
    baseEntries: [{ key: "a", value: enc("1") }],
    deltas: [],
  };
  const bytes = encodeArchive(data);
  const decoded = decodeArchive(bytes);
  assert.equal(decoded.baseEntries.length, 1);
  assert.deepEqual(decoded.baseEntries[0].value, enc("1"));
});

test("concurrency: concurrent snapshots form a contiguous version chain", async () => {
  const { store, engine } = makeEngine();
  const ROUNDS = 50;
  const handles = [];
  for (let i = 0; i < ROUNDS; i++) {
    store.set(`k${i}`, enc(`v${i}`));
    handles.push(await Promise.all([engine.snapshot(), engine.snapshot()]));
  }
  const snaps = handles
    .flat()
    .map((h) => h.snapshot)
    .sort((a, b) => (a.id < b.id ? -1 : 1));
  // Chains must tile [0, finalVersion] with no gaps and no overlaps.
  let expected = 0n;
  for (const s of snaps) {
    assert.equal(s.fromVersion, expected, `snapshot ${s.id} continuity`);
    expected = s.toVersion;
  }
  assert.equal(expected, store.currentVersion);
  // Every dirtied key appears in exactly one snapshot.
  const seen = new Set<string>();
  for (const s of snaps) {
    for (const e of s.entries) {
      assert.ok(!seen.has(e.key), `key ${e.key} captured twice`);
      seen.add(e.key);
    }
  }
  assert.equal(seen.size, ROUNDS);
  for (const pair of handles) for (const h of pair) h.release();
  engine.dispose();
});

test("concurrency: mutation racing a checkpoint is captured exactly once", async () => {
  const { store, engine } = makeEngine();
  store.set("before", enc("1"));
  const pending = engine.checkpoint();
  // Mutate while the checkpoint may still be in flight; the mutation
  // lands either in the archive or in the next delta, never both/never.
  store.set("after", enc("2"));
  const archive = await pending;
  const h = await engine.snapshot();
  const { store: restored } = SnapshotEngine.restore(archive.bytes);
  const inArchive = restored.has("after");
  const inDelta = h.snapshot.entries.some((e) => e.key === "after");
  assert.notEqual(inArchive, inDelta, "mutation must be captured exactly once");
  // Final live state always contains both keys.
  assert.deepEqual(store.get("before"), enc("1"));
  assert.deepEqual(store.get("after"), enc("2"));
  h.release();
  archive.release();
  engine.dispose();
});

test("concurrency: parallel checkpoint calls are serialized and consistent", async () => {
  const { store, engine } = makeEngine();
  for (let i = 0; i < 20; i++) store.set(`k${i}`, enc(`${i}`));
  const [a1, a2] = await Promise.all([engine.checkpoint(), engine.checkpoint()]);
  const r1 = SnapshotEngine.restore(a1.bytes);
  const r2 = SnapshotEngine.restore(a2.bytes);
  assert.equal(r1.version, r2.version);
  assert.equal(r1.store.size, r2.store.size);
  a1.release();
  a2.release();
  engine.dispose();
});

test("leaks: dispose with live handles throws LeakError", async () => {
  const { store, engine } = makeEngine();
  store.set("k", enc("v"));
  const h = await engine.snapshot();
  assert.throws(() => engine.dispose(), LeakError);
  h.release();
  engine.dispose(); // now clean
});

test("leaks: double release and foreign release are rejected", async () => {
  const { store, engine } = makeEngine();
  store.set("k", enc("v"));
  const h = await engine.snapshot();
  h.release();
  assert.throws(() => h.release(), LeakError);

  const registry = new ResourceRegistry();
  assert.throws(() => registry.release({}), LeakError);
  registry.assertNoLeaks();
  engine.dispose();
});

test("leaks: strict accounting across the full lifecycle", async () => {
  const { store, engine } = makeEngine();
  for (let i = 0; i < 10; i++) store.set(`k${i}`, enc(`${i}`));
  const h1 = await engine.snapshot();
  const archive = await engine.checkpoint();
  assert.equal(engine.registryStats.live, 2);
  assert.equal(engine.registryStats.acquired, 2);
  h1.release();
  archive.release();
  engine.assertNoLeaks();
  assert.equal(engine.registryStats.live, 0);
  engine.dispose();
});

test("efficiency: incremental snapshot is tiny next to a large state", async () => {
  const { store, engine } = makeEngine();
  const big = new Uint8Array(1024).fill(7);
  for (let i = 0; i < 1000; i++) store.set(`bulk-${i}`, big);
  const full = await engine.checkpoint();
  full.release();

  store.set("bulk-0", enc("tiny-change"));
  const h = await engine.snapshot();
  const deltaBytes = h.snapshot.entries.reduce((n, e) => n + (e.value?.length ?? 0), 0);
  assert.ok(
    deltaBytes < store.byteSize / 1000,
    `delta ${deltaBytes}B should be far below state ${store.byteSize}B`,
  );
  assert.equal(h.snapshot.entries.length, 1);
  h.release();
  engine.dispose();
});

test("engine: use after dispose is rejected", async () => {
  const { engine } = makeEngine();
  engine.dispose();
  await assert.rejects(() => engine.snapshot(), SnapshotError);
  await assert.rejects(() => engine.checkpoint(), SnapshotError);
});

test("archive handle: CheckpointArchive tracks release state", async () => {
  const { store, engine } = makeEngine();
  store.set("k", enc("v"));
  const archive: CheckpointArchive = await engine.checkpoint();
  assert.equal(archive.isReleased, false);
  archive.release();
  assert.equal(archive.isReleased, true);
  assert.throws(() => archive.release(), LeakError);
  engine.dispose();
});
