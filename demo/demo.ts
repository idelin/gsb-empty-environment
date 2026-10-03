import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { performance } from "perf_hooks";
import { CheckpointInfo, CheckpointManager } from "../src/checkpoint";
import { chunkFileName, checkpointDirName } from "../src/checkpoint";
import { Manifest } from "../src/checkpoint";
import { CorruptionError, restoreState } from "../src/restore";
import { StateTree } from "../src/stateTree";

const TOTAL_KEYS = 100000;
const PAGE_CAPACITY = 256;
const PAGES_PER_CHUNK = 16;

function pad(n: number): string {
  let s = String(n);
  while (s.length < 8) {
    s = "0" + s;
  }
  return s;
}

const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

function percentile(sorted: number[], q: number): number {
  return sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))];
}

function reportLatencies(label: string, samples: number[]): void {
  if (samples.length === 0) {
    console.log("  " + label + ": (no samples)");
    return;
  }
  const sorted = samples.slice().sort((a, b) => a - b);
  console.log(
    "  " +
      label +
      ": p50=" +
      percentile(sorted, 0.5).toFixed(3) +
      "ms p95=" +
      percentile(sorted, 0.95).toFixed(3) +
      "ms p99=" +
      percentile(sorted, 0.99).toFixed(3) +
      "ms max=" +
      sorted[sorted.length - 1].toFixed(3) +
      "ms"
  );
}

function reportCheckpoint(info: CheckpointInfo): void {
  console.log(
    "  cp-" +
      info.id +
      ": seq=" +
      info.seq +
      " keys=" +
      info.keyCount +
      " pages=" +
      info.pageCount +
      " chunks=" +
      info.chunkCount +
      " stored=" +
      info.chunksStored +
      " reused=" +
      info.chunksReused +
      " bytesWritten=" +
      info.bytesWritten +
      " took=" +
      info.durationMs +
      "ms"
  );
}

async function main(): Promise<void> {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ckpt-demo-"));
  console.log("checkpoint dir: " + dir + "\n");

  const tree = new StateTree(PAGE_CAPACITY);
  const mgr = new CheckpointManager(tree, dir, { pagesPerChunk: PAGES_PER_CHUNK });

  console.log("[phase 1] full baseline checkpoint over 20k keys");
  for (let i = 0; i < 20000; i++) {
    tree.set("key-" + pad(i), "value-" + i);
  }
  reportCheckpoint(await mgr.createCheckpoint());

  console.log("\n[phase 2] continuous writes to 100k keys, checkpoint triggered at 50k");
  const latBaseline: number[] = [];
  const latDuringCp: number[] = [];
  let cpPromise: Promise<CheckpointInfo> | null = null;
  let cpRunning = false;
  for (let i = 20000; i < TOTAL_KEYS; i++) {
    const start = performance.now();
    tree.set("key-" + pad(i), "value-" + i);
    const elapsed = performance.now() - start;
    if (cpPromise === null) {
      latBaseline.push(elapsed);
    } else if (cpRunning) {
      latDuringCp.push(elapsed);
    }
    if (i === 50000) {
      cpPromise = mgr.createCheckpoint();
      cpRunning = true;
      cpPromise.then(() => {
        cpRunning = false;
      });
      console.log("  -> checkpoint cp-2 triggered at key " + i + ", writes continue");
    }
    if (i % 256 === 0) {
      await tick();
    }
  }
  const cp2 = await cpPromise!;
  reportCheckpoint(cp2);
  console.log("  incremental delta: only " + cp2.chunksStored + "/" + cp2.chunkCount + " chunks rewritten");
  reportLatencies("write latency before checkpoint ", latBaseline);
  reportLatencies("write latency during checkpoint ", latDuringCp);

  console.log("\n[phase 3] more writes + final incremental checkpoint cp-3");
  for (let i = 0; i < 5000; i++) {
    tree.set("key-" + pad(i), "updated-" + i);
    tree.set("new-" + pad(i), "fresh-" + i);
  }
  const cp3 = await mgr.createCheckpoint();
  reportCheckpoint(cp3);

  console.log("\n[phase 4] verified recovery from cp-3 (hash chain + page hashes)");
  const restored = restoreState(dir, cp3.id);
  console.log(
    "  restored seq=" +
      restored.seq +
      " keys=" +
      restored.state.size +
      " verifiedChunks=" +
      restored.verifiedChunks +
      " verifiedPages=" +
      restored.verifiedPages
  );

  console.log("\n[phase 5] corrupt the 3rd chunk stored by cp-3 and recover again");
  const manifest = JSON.parse(
    fs.readFileSync(path.join(dir, checkpointDirName(cp3.id), "manifest.json"), "utf8")
  ) as Manifest;
  const storedChunks = manifest.chunks.filter((c) => c.stored);
  const chunkEntry = storedChunks[2];
  const victim = path.join(dir, checkpointDirName(cp3.id), chunkFileName(chunkEntry.index));
  const buf = fs.readFileSync(victim);
  // Flip value bytes of the first entry in the chunk's first page.
  const firstKeyLen = buf.readUInt32LE(56);
  const valueOffset = 64 + firstKeyLen;
  for (let i = 0; i < 4; i++) {
    buf[valueOffset + i] = buf[valueOffset + i] ^ 0xff;
  }
  fs.writeFileSync(victim, buf);
  console.log(
    "  corrupted 4 value bytes at offset " + valueOffset +
      " of " + path.relative(dir, victim) + " (chunk index " + chunkEntry.index + ")"
  );

  try {
    restoreState(dir, cp3.id);
    console.log("  ERROR: corrupted checkpoint was accepted!");
    process.exitCode = 1;
  } catch (err) {
    if (err instanceof CorruptionError) {
      const d = err.details;
      console.log("  recovery rejected bad data: " + err.message);
      console.log("  expected chunk hash: " + d.expectedChunkHash.slice(0, 16) + "...");
      console.log("  actual   chunk hash: " + d.actualChunkHash.slice(0, 16) + "...");
      for (const page of d.badPages) {
        console.log(
          "  bad page " +
            page.pageId +
            ": keys [" +
            page.firstKey +
            " .. " +
            page.lastKey +
            "] (" +
            page.keyCount +
            " keys)"
        );
        console.log(
          "    expected page hash: " + page.expectedHash.slice(0, 16) + "... actual: " +
            (page.actualHash === null ? "<unparseable>" : page.actualHash.slice(0, 16) + "...")
        );
      }
    } else {
      throw err;
    }
  }

  console.log("\n[phase 6] fall back to uncorrupted cp-2, recovery succeeds");
  const fallback = restoreState(dir, cp2.id);
  console.log("  restored cp-2: seq=" + fallback.seq + " keys=" + fallback.state.size);
  console.log("\ndemo finished OK");
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
