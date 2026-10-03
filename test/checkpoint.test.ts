import { strict as assert } from "assert";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { performance } from "perf_hooks";
import test from "node:test";
import { CheckpointManager, chunkFileName, checkpointDirName, Manifest } from "../src/checkpoint";
import { CorruptionError, HashChainError, restoreState } from "../src/restore";
import { sha256Hex } from "../src/hash";
import { StateTree } from "../src/stateTree";

function tmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "ckpt-test-"));
}

function rmrf(dir: string): void {
  if (!fs.existsSync(dir)) {
    return;
  }
  for (const entry of fs.readdirSync(dir)) {
    const p = path.join(dir, entry);
    const stat = fs.lstatSync(p);
    if (stat.isDirectory()) {
      rmrf(p);
    } else {
      fs.unlinkSync(p);
    }
  }
  fs.rmdirSync(dir);
}

const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

function pad(n: number): string {
  let s = String(n);
  while (s.length < 8) {
    s = "0" + s;
  }
  return s;
}

test("snapshot is consistent while writes continue during checkpoint", async () => {
  const dir = tmpDir();
  try {
    const tree = new StateTree(8);
    const mgr = new CheckpointManager(tree, dir, { pagesPerChunk: 4, workers: 4 });
    const log: Array<{ key: string; value: string; seq: number }> = [];
    const write = (key: string, value: string) => {
      log.push({ key, value, seq: tree.set(key, value) });
    };

    for (let i = 0; i < 500; i++) {
      write("key-" + pad(i), "value-" + i);
    }
    const cpPromise = mgr.createCheckpoint();
    // Keep writing (including overwrites) while the checkpoint is generated.
    for (let i = 500; i < 1000; i++) {
      write("key-" + pad(i), "value-" + i);
      if (i % 50 === 0) {
        await tick();
      }
    }
    for (let i = 0; i < 500; i += 7) {
      write("key-" + pad(i), "updated-" + i);
    }
    const info = await cpPromise;

    const expected = new Map<string, string>();
    for (const entry of log) {
      if (entry.seq <= info.seq) {
        expected.set(entry.key, entry.value);
      }
    }
    const restored = restoreState(dir, info.id);
    assert.equal(restored.seq, info.seq);
    assert.equal(restored.state.size, expected.size);
    for (const [key, value] of expected) {
      assert.equal(restored.state.get(key), value, "value mismatch for " + key);
    }
  } finally {
    rmrf(dir);
  }
});

test("incremental checkpoint stores only changed chunks and links the hash chain", async () => {
  const dir = tmpDir();
  try {
    const tree = new StateTree(8);
    const mgr = new CheckpointManager(tree, dir, { pagesPerChunk: 4, workers: 2 });
    for (let i = 0; i < 400; i++) {
      tree.set("key-" + pad(i), "value-" + i);
    }
    const cp1 = await mgr.createCheckpoint();
    assert.equal(cp1.chunksReused, 0);
    assert.ok(cp1.chunksStored > 0);

    // Touch only a few keys -> only the tail page/chunk changes.
    tree.set("key-00000399", "changed");
    tree.set("key-00000398", "changed");
    const cp2 = await mgr.createCheckpoint();
    assert.ok(cp2.chunksStored < cp1.chunksStored, "delta should store fewer chunks");
    assert.ok(cp2.chunksReused > 0, "unchanged chunks should be reused");

    const cp1Raw = fs.readFileSync(path.join(dir, checkpointDirName(cp1.id), "manifest.json"), "utf8");
    const cp2Manifest = JSON.parse(
      fs.readFileSync(path.join(dir, checkpointDirName(cp2.id), "manifest.json"), "utf8")
    ) as Manifest;
    assert.equal(cp2Manifest.prevManifestHash, sha256Hex(cp1Raw));

    const cp2ChunkFiles = fs.readdirSync(path.join(dir, checkpointDirName(cp2.id))).filter((f) => f.startsWith("chunk-"));
    assert.equal(cp2ChunkFiles.length, cp2.chunksStored);

    const restored = restoreState(dir, cp2.id);
    assert.equal(restored.state.size, 400);
    assert.equal(restored.state.get("key-00000399"), "changed");
    assert.equal(restored.state.get("key-00000000"), "value-0");
  } finally {
    rmrf(dir);
  }
});

test("corrupting the 3rd chunk is precisely located and rejected on recovery", async () => {
  const dir = tmpDir();
  try {
    const tree = new StateTree(8);
    const mgr = new CheckpointManager(tree, dir, { pagesPerChunk: 4, workers: 2 });
    for (let i = 0; i < 128; i++) {
      tree.set("key-" + pad(i), "value-" + i);
    }
    const cp = await mgr.createCheckpoint();
    assert.ok(cp.chunkCount >= 3, "need at least 3 chunks for this test");

    // Sanity: clean restore works before corruption.
    const ok = restoreState(dir, cp.id);
    assert.equal(ok.state.size, 128);

    // Corrupt the 3rd chunk (index 2): flip value bytes of its first entry.
    // Layout: 12B chunk header, 12B page header, 32B page hash, then entries.
    const victim = path.join(dir, checkpointDirName(cp.id), chunkFileName(2));
    const buf = fs.readFileSync(victim);
    const firstKeyLen = buf.readUInt32LE(56);
    const valueOffset = 64 + firstKeyLen;
    for (let i = 0; i < 4; i++) {
      buf[valueOffset + i] = buf[valueOffset + i] ^ 0xff;
    }
    fs.writeFileSync(victim, buf);

    let caught: CorruptionError | null = null;
    try {
      restoreState(dir, cp.id);
    } catch (err) {
      caught = err as CorruptionError;
    }
    assert.ok(caught instanceof CorruptionError, "restore must reject corrupted data");
    assert.equal(caught!.details.checkpointId, cp.id);
    assert.equal(caught!.details.chunkIndex, 2, "must locate the 3rd chunk exactly");
    assert.notEqual(caught!.details.actualChunkHash, caught!.details.expectedChunkHash);
    assert.ok(caught!.details.badPages.length > 0, "must locate bad pages");
    for (const page of caught!.details.badPages) {
      // 3rd chunk covers pages [8, 12) with pagesPerChunk=4.
      assert.ok(page.pageId >= 8 && page.pageId < 12, "bad page must fall inside chunk 2");
      assert.notEqual(page.actualHash, page.expectedHash);
    }
    const badPage = caught!.details.badPages[0];
    assert.ok(badPage.firstKey !== null && badPage.lastKey !== null, "key range reported");
  } finally {
    rmrf(dir);
  }
});

test("tampering with an earlier manifest breaks the hash chain", async () => {
  const dir = tmpDir();
  try {
    const tree = new StateTree(8);
    const mgr = new CheckpointManager(tree, dir, { pagesPerChunk: 4, workers: 2 });
    for (let i = 0; i < 100; i++) {
      tree.set("key-" + pad(i), "value-" + i);
    }
    await mgr.createCheckpoint();
    for (let i = 100; i < 200; i++) {
      tree.set("key-" + pad(i), "value-" + i);
    }
    const cp2 = await mgr.createCheckpoint();

    const cp1ManifestPath = path.join(dir, checkpointDirName(1), "manifest.json");
    const tampered = JSON.parse(fs.readFileSync(cp1ManifestPath, "utf8")) as Manifest;
    tampered.keyCount = 999999;
    fs.writeFileSync(cp1ManifestPath, JSON.stringify(tampered, null, 2));

    assert.throws(() => restoreState(dir, cp2.id), HashChainError);
  } finally {
    rmrf(dir);
  }
});

test("write latency jitter stays bounded while checkpointing 100k keys", async () => {
  const dir = tmpDir();
  try {
    const tree = new StateTree(256);
    const mgr = new CheckpointManager(tree, dir, { pagesPerChunk: 16 });
    const total = 100000;
    const latencies = new Float64Array(total);
    let cpPromise: Promise<unknown> | null = null;
    for (let i = 0; i < total; i++) {
      const start = performance.now();
      tree.set("key-" + pad(i), "value-" + i);
      latencies[i] = performance.now() - start;
      if (i === total / 2) {
        cpPromise = mgr.createCheckpoint();
      }
      if (i % 512 === 0) {
        await tick();
      }
    }
    await cpPromise;

    const sorted = Array.from(latencies).sort((a, b) => a - b);
    const p50 = sorted[Math.floor(total * 0.5)];
    const p99 = sorted[Math.floor(total * 0.99)];
    const max = sorted[total - 1];
    console.log(
      "    latency ms: p50=" + p50 + " p99=" + p99.toFixed(3) + " max=" + max.toFixed(3)
    );
    assert.ok(p99 < 50, "p99 write latency must stay bounded, got " + p99 + "ms");
    assert.ok(max < 250, "max write latency must stay bounded, got " + max + "ms");

    const restored = restoreState(dir);
    assert.equal(restored.state.size, 50001);
  } finally {
    rmrf(dir);
  }
});
