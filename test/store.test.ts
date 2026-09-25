import { test } from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { BlockStore } from "../src/store.ts";
import {
  TiledImage,
  deleteSnapshot,
  imageRoot,
  snapshotRoot,
  snapshotTiles,
} from "../src/image.ts";
import {
  blankPixels,
  checkerPixels,
  gradientPixels,
  pixelsEqual,
  solidPixels,
} from "../src/pixels.ts";
import { constantDigest } from "../src/hash.ts";

const TILE = 8;
const BLANK = blankPixels(TILE);
const CHECKER = checkerPixels(TILE, [255, 0, 0, 255], [0, 0, 255, 255]);
const GRAD1 = gradientPixels(TILE, 1);
const GRAD2 = gradientPixels(TILE, 2);
const SOLID = solidPixels(TILE, [9, 9, 9, 255]);

async function tmpdir(): Promise<string> {
  return fs.mkdtemp(path.join(os.tmpdir(), "tilestore-"));
}

async function cleanup(dir: string): Promise<void> {
  await fs.rm(dir, { recursive: true, force: true });
}

function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s / 0x100000000;
  };
}

test("dedup: identical content shares one block, blank tiles share across images", async (t) => {
  const dir = await tmpdir();
  t.after(() => cleanup(dir));
  const store = await BlockStore.open(dir);
  const a = await TiledImage.createBlank(store, "A", 16, 16, TILE);
  const b = await TiledImage.createBlank(store, "B", 16, 16, TILE);
  assert.equal(store.stats().blocks, 1);
  assert.equal(a.tileId(0, 0), b.tileId(0, 0));
  assert.equal(store.refsOf(a.tileId(0, 0)), 8);
  const again = await store.intern(blankPixels(TILE));
  assert.equal(again, a.tileId(0, 0));
  assert.equal(store.stats().blocks, 1);
  assert.ok(store.verify().ok);
});

test("collision: forced digest collision falls back to full pixel compare", async (t) => {
  const dir = await tmpdir();
  t.after(() => cleanup(dir));
  const store = await BlockStore.open(dir, { digest: constantDigest });
  const idA = await store.intern(CHECKER);
  const idB = await store.intern(GRAD1);
  const idC = await store.intern(GRAD2);
  assert.notEqual(idA, idB);
  assert.notEqual(idB, idC);
  assert.equal(store.bucketSize(store.digestOf(idA)), 3);
  assert.ok(pixelsEqual(store.read(idA), CHECKER));
  assert.ok(pixelsEqual(store.read(idB), GRAD1));
  assert.ok(pixelsEqual(store.read(idC), GRAD2));
  assert.equal(await store.intern(CHECKER.slice()), idA);
  const img = await TiledImage.createBlank(store, "img", 8, 8, TILE);
  await img.setTile(0, 0, CHECKER);
  assert.ok(pixelsEqual(await img.getTile(0, 0), CHECKER));
  await img.setTile(0, 0, GRAD2);
  assert.ok(pixelsEqual(await img.getTile(0, 0), GRAD2));
  assert.ok(store.verify().ok);
});

test("copy-on-write: two layers share a block, editing one leaves the other intact", async (t) => {
  const dir = await tmpdir();
  t.after(() => cleanup(dir));
  const store = await BlockStore.open(dir);
  const a = await TiledImage.createBlank(store, "A", 16, 16, TILE);
  const b = await TiledImage.createBlank(store, "B", 16, 16, TILE);
  const blankId = a.tileId(0, 0);
  const checkerId = await a.setTile(0, 0, CHECKER);
  assert.equal(b.tileId(0, 0), blankId);
  assert.ok(pixelsEqual(await b.getTile(0, 0), BLANK));
  assert.equal(a.tileId(0, 0), checkerId);
  assert.equal(a.tileId(1, 0), b.tileId(1, 0));
  assert.equal(store.refsOf(blankId), 7);
  assert.equal(store.refsOf(checkerId), 1);
  const rendered = await a.render();
  assert.equal(rendered[0], 255);
  assert.equal(rendered[2], 0);
  const blankPxOff = (0 * 16 + 8) * 4;
  assert.equal(rendered[blankPxOff + 3], 0);
  assert.ok(store.verify().ok);
});

test("snapshot pins old blocks across later edits and GC", async (t) => {
  const dir = await tmpdir();
  t.after(() => cleanup(dir));
  const store = await BlockStore.open(dir);
  const a = await TiledImage.createBlank(store, "A", 8, 8, TILE);
  const p1 = await a.setTile(0, 0, GRAD1);
  await a.snapshot("s1");
  const p2 = await a.setTile(0, 0, GRAD2);
  assert.equal(snapshotTiles(store, "s1")[0], p1);
  assert.equal(a.tileId(0, 0), p2);
  await store.gc();
  await store.gc();
  assert.ok(store.has(p1));
  assert.ok(pixelsEqual(store.read(snapshotTiles(store, "s1")[0]), GRAD1));
  assert.ok(store.verify().ok);
});

test("deleteSnapshot reclaims only blocks with no remaining references", async (t) => {
  const dir = await tmpdir();
  t.after(() => cleanup(dir));
  const store = await BlockStore.open(dir);
  const a = await TiledImage.createBlank(store, "A", 16, 16, TILE);
  const blankId = a.tileId(0, 0);
  const checkerId = await a.setTile(0, 0, CHECKER);
  await a.snapshot("s1");
  const gradId = await a.setTile(0, 0, GRAD1);
  await deleteSnapshot(store, "s1");
  await store.gc();
  const r2 = await store.gc();
  assert.deepEqual(r2.reclaimed, [checkerId]);
  assert.ok(store.has(gradId));
  assert.ok(store.has(blankId));
  assert.ok(store.verify().ok);
});

test("generational GC: orphan survives its first cycle, reclaimed on the next", async (t) => {
  const dir = await tmpdir();
  t.after(() => cleanup(dir));
  const store = await BlockStore.open(dir);
  const orphan = await store.intern(GRAD1);
  const r1 = await store.gc();
  assert.deepEqual(r1.marked, [orphan]);
  assert.deepEqual(r1.reclaimed, []);
  assert.ok(store.has(orphan));
  const r2 = await store.gc();
  assert.deepEqual(r2.reclaimed, [orphan]);
  assert.ok(!store.has(orphan));
  const committed = await store.intern(GRAD2);
  await store.gc();
  await store.registerRoot("manual", [committed]);
  await store.gc();
  await store.gc();
  assert.ok(store.has(committed));
  assert.ok(store.verify().ok);
});

test("read lease blocks reclamation until released", async (t) => {
  const dir = await tmpdir();
  t.after(() => cleanup(dir));
  const store = await BlockStore.open(dir);
  const doomed = await store.intern(GRAD1);
  const lease = store.lease(doomed);
  await store.gc();
  await store.gc();
  assert.ok(store.has(doomed));
  lease.release();
  await store.gc();
  assert.ok(store.has(doomed));
  const r = await store.gc();
  assert.deepEqual(r.reclaimed, [doomed]);
  assert.throws(() => lease.release(), /already released/);
  assert.ok(store.verify().ok);
});

test("ledger is cross-checked by independent root traversal, GC never trusts refcounts", async (t) => {
  const dir = await tmpdir();
  t.after(() => cleanup(dir));
  const store = await BlockStore.open(dir);
  const a = await TiledImage.createBlank(store, "A", 8, 8, TILE);
  const blankId = a.tileId(0, 0);
  const id = await a.setTile(0, 0, CHECKER);
  assert.ok(store.verify().ok);
  store.debugTamperRefs(id, 3);
  const bad = store.verify();
  assert.ok(!bad.ok);
  assert.match(bad.errors[0], /ledger mismatch/);
  store.debugTamperRefs(id, -3);
  assert.ok(store.verify().ok);
  await store.removeRoot(imageRoot("A"));
  store.debugTamperRefs(id, 5);
  await store.gc();
  const r = await store.gc();
  assert.deepEqual(r.reclaimed.sort(), [id, blankId].sort());
  assert.ok(store.verify().ok);
});

test("crash safety: every committed state on disk is self-consistent", async (t) => {
  const dir = await tmpdir();
  t.after(() => cleanup(dir));
  const store = await BlockStore.open(dir);
  const a = await TiledImage.createBlank(store, "A", 16, 16, TILE);
  const ops: Array<() => Promise<void>> = [
    () => a.setTile(0, 0, CHECKER).then(() => {}),
    () => a.snapshot("s1"),
    () => a.setTile(1, 1, GRAD1).then(() => {}),
    () => store.intern(SOLID).then(() => {}),
    () => store.gc().then(() => {}),
    () => deleteSnapshot(store, "s1"),
    () => store.gc().then(() => {}),
    () => store.gc().then(() => {}),
  ];
  for (const op of ops) {
    await op();
    const reopened = await BlockStore.open(dir);
    const v = reopened.verify();
    assert.ok(v.ok, JSON.stringify(v.errors));
    for (const root of reopened.rootNames()) {
      for (const id of reopened.rootTiles(root)) {
        assert.ok(reopened.has(id), `live root ${root} dangles to ${id}`);
      }
    }
  }
  const orphan = await store.intern(GRAD2);
  const crashed = await BlockStore.open(dir);
  assert.ok(crashed.has(orphan));
  await crashed.gc();
  const r = await crashed.gc();
  assert.deepEqual(r.reclaimed, [orphan]);
  assert.ok(crashed.verify().ok);
});

test("interleavings: exhaustive op sequences keep invariants and survive reopen", async (t) => {
  const patterns = [CHECKER, GRAD1, GRAD2];
  const ops = ["W", "R", "L", "G"] as const;
  const sequences: string[][] = [];
  const build = (prefix: string[], depth: number) => {
    if (depth === 0) {
      sequences.push(prefix);
      return;
    }
    for (const op of ops) build([...prefix, op], depth - 1);
  };
  for (let len = 1; len <= 3; len++) build([], len);
  assert.equal(sequences.length, 84);
  for (const seq of sequences) {
    const dir = await tmpdir();
    const store = await BlockStore.open(dir, {
      digest: constantDigest,
      durability: "atomic-rename",
    });
    const a = await TiledImage.createBlank(store, "A", 8, 8, TILE);
    const b = await TiledImage.createBlank(store, "B", 8, 8, TILE);
    let writes = 0;
    let snapOn = false;
    let lease: { release(): void } | null = null;
    for (const op of seq) {
      if (op === "W") {
        await a.setTile(0, 0, patterns[writes++ % patterns.length]);
      } else if (op === "R") {
        if (snapOn) {
          await deleteSnapshot(store, "s0");
          snapOn = false;
        } else {
          await a.snapshot("s0");
          snapOn = true;
        }
      } else if (op === "L") {
        if (lease) {
          lease.release();
          lease = null;
        } else {
          lease = store.lease(a.tileId(0, 0));
        }
      } else {
        await store.gc();
      }
      const v = store.verify();
      assert.ok(v.ok, `seq=${seq.join("")} op=${op}: ${v.errors.join("; ")}`);
      assert.equal(a.tileId(0, 0) === b.tileId(0, 0), writes === 0);
    }
    if (lease) lease.release();
    const reopened = await BlockStore.open(dir, {
      digest: constantDigest,
      durability: "atomic-rename",
    });
    const v = reopened.verify();
    assert.ok(v.ok, `seq=${seq.join("")} reopen: ${v.errors.join("; ")}`);
    await cleanup(dir);
  }
});

test("interleavings: seeded random long sequences with reopen after every op", async (t) => {
  const patterns = [CHECKER, GRAD1, GRAD2, SOLID];
  for (let seed = 1; seed <= 12; seed++) {
    const rand = lcg(seed * 7919);
    const dir = await tmpdir();
    const store = await BlockStore.open(dir, { durability: "atomic-rename" });
    const a = await TiledImage.createBlank(store, "A", 16, 16, TILE);
    const b = await TiledImage.createBlank(store, "B", 16, 16, TILE);
    const leases: Array<{ release(): void }> = [];
    const snaps = new Set<string>();
    for (let step = 0; step < 40; step++) {
      const pick = Math.floor(rand() * 7);
      if (pick === 0) {
        const img = rand() < 0.5 ? a : b;
        await img.setTile(
          Math.floor(rand() * 2),
          Math.floor(rand() * 2),
          patterns[Math.floor(rand() * patterns.length)],
        );
      } else if (pick === 1) {
        const name = "s" + Math.floor(rand() * 3);
        if (snaps.has(name)) {
          await deleteSnapshot(store, name);
          snaps.delete(name);
        } else {
          await (rand() < 0.5 ? a : b).snapshot(name);
          snaps.add(name);
        }
      } else if (pick === 2) {
        const img = rand() < 0.5 ? a : b;
        leases.push(store.lease(img.tileId(Math.floor(rand() * 2), Math.floor(rand() * 2))));
      } else if (pick === 3) {
        if (leases.length > 0) leases.pop()!.release();
      } else if (pick === 4) {
        await store.gc();
      } else if (pick === 5) {
        await store.intern(patterns[Math.floor(rand() * patterns.length)]);
      } else {
        await store.gc();
        await store.gc();
      }
      const v = store.verify();
      assert.ok(v.ok, `seed=${seed} step=${step}: ${v.errors.join("; ")}`);
      const reopened = await BlockStore.open(dir, { durability: "atomic-rename" });
      const rv = reopened.verify();
      assert.ok(rv.ok, `seed=${seed} step=${step} reopen: ${rv.errors.join("; ")}`);
      for (const root of reopened.rootNames()) {
        for (const id of reopened.rootTiles(root)) {
          assert.ok(reopened.has(id), `seed=${seed} step=${step}: ${root} dangles`);
        }
      }
    }
    for (const l of leases) l.release();
    await cleanup(dir);
  }
});
