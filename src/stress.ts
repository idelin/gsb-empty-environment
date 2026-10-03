/**
 * Orchestrator for the acceptance scenario:
 *   1 writer thread densely updating 1000 keys via multi-key transfer
 *   transactions (global sum conserved), 5 concurrent reader threads running
 *   full-table checksum scans against lock-free snapshots.
 */
import { Worker } from "node:worker_threads";
import { createStore, MvccReader } from "./mvcc.ts";

export interface StressOptions {
  keyCount?: number;
  txCount?: number;
  readerCount?: number;
  baseValue?: number;
  seed?: number;
}

export interface StressReport {
  keyCount: number;
  txCount: number;
  readerCount: number;
  expectedSum: number;
  finalSum: number;
  writerElapsedMs: number;
  writerTxPerSec: number;
  totalScans: number;
  totalViolations: number;
  tornSnapshots: Array<{ snap: number; sum: number }>;
  readers: Array<{ scans: number; violations: number; elapsedMs: number }>;
}

export async function runStress(options: StressOptions = {}): Promise<StressReport> {
  const keyCount = options.keyCount ?? 1000;
  const txCount = options.txCount ?? 300_000;
  const readerCount = options.readerCount ?? 5;
  const baseValue = options.baseValue ?? 1000;
  const seed = options.seed ?? 0x9e3779b9;

  // Each transaction appends exactly 2 version nodes.
  const poolSize = txCount * 2 + 16;
  const store = createStore(keyCount, poolSize);
  store.baseVal.fill(baseValue);
  const expectedSum = keyCount * baseValue;

  const tornSnapshots: Array<{ snap: number; sum: number }> = [];
  const readers: StressReport["readers"] = [];
  let writerElapsedMs = 0;

  const workers: Promise<void>[] = [];

  workers.push(
    new Promise((resolve, reject) => {
      const w = new Worker(new URL("./stress-worker.ts", import.meta.url), {
        workerData: { sab: store.sab, keyCount, poolSize, role: "writer", txCount, expectedSum, seed },
      });
      w.once("message", (msg) => {
        writerElapsedMs = msg.elapsedMs;
        resolve();
      });
      w.once("error", reject);
    }),
  );

  for (let i = 0; i < readerCount; i++) {
    workers.push(
      new Promise((resolve, reject) => {
        const w = new Worker(new URL("./stress-worker.ts", import.meta.url), {
          workerData: {
            sab: store.sab,
            keyCount,
            poolSize,
            role: "reader",
            txCount,
            expectedSum,
            seed: seed + i + 1,
          },
        });
        w.on("message", (msg) => {
          if (msg.torn) {
            tornSnapshots.push(msg.torn);
          } else {
            readers.push({ scans: msg.scans, violations: msg.violations, elapsedMs: msg.elapsedMs });
            resolve();
          }
        });
        w.once("error", reject);
      }),
    );
  }

  await Promise.all(workers);

  // Final validation on the main thread: latest snapshot must conserve the sum.
  const reader = new MvccReader(store);
  const finalSum = reader.checksum(reader.snapshot());

  const totalScans = readers.reduce((acc, r) => acc + r.scans, 0);
  const totalViolations = readers.reduce((acc, r) => acc + r.violations, 0);

  return {
    keyCount,
    txCount,
    readerCount,
    expectedSum,
    finalSum,
    writerElapsedMs,
    writerTxPerSec: Math.round(txCount / (writerElapsedMs / 1000)),
    totalScans,
    totalViolations,
    tornSnapshots,
    readers,
  };
}
