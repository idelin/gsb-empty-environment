import { UploadProgressModel } from "../src/upload-progress";
import type { ProgressSnapshot } from "../src/upload-progress";

let failures = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    console.log("ok - " + name);
  } catch (error) {
    failures += 1;
    console.error("FAIL - " + name);
    console.error(error);
  }
}

function assertEqual(actual: unknown, expected: unknown, message: string): void {
  const actualJson = JSON.stringify(actual);
  const expectedJson = JSON.stringify(expected);
  if (actualJson !== expectedJson) {
    throw new Error(message + " -- expected " + expectedJson + " but got " + actualJson);
  }
}

function assertThrows(fn: () => void, message: string): void {
  let threw = false;
  try {
    fn();
  } catch (error) {
    threw = true;
  }
  if (!threw) {
    throw new Error(message + " -- expected an exception but none was thrown");
  }
}

function assertInvariant(model: UploadProgressModel): void {
  const snap = model.snapshot();
  if (snap.confirmedBytes > snap.totalBytes) {
    throw new Error("confirmedBytes exceeds totalBytes");
  }
  if (snap.transferredBytes > snap.totalBytes) {
    throw new Error("transferredBytes exceeds totalBytes");
  }
  const expectedTransferred = Math.min(snap.confirmedBytes + snap.tentativeBytes, snap.totalBytes);
  assertEqual(snap.transferredBytes, expectedTransferred, "transferredBytes must cap at totalBytes");
  const unique = new Set(snap.confirmedChunks);
  assertEqual(unique.size, snap.confirmedChunks.length, "confirmedChunks must not contain duplicates");
}

const TOTAL = 1000;
const CHUNK = 256;

test("rejects invalid constructor arguments", () => {
  assertThrows(() => new UploadProgressModel(-1, CHUNK), "negative totalBytes");
  assertThrows(() => new UploadProgressModel(TOTAL, 0), "zero chunkSize");
  assertThrows(() => new UploadProgressModel(TOTAL, -5), "negative chunkSize");
  assertThrows(() => new UploadProgressModel(Number.NaN, CHUNK), "NaN totalBytes");
});

test("reports a zero-length upload as immediately complete", () => {
  const model = new UploadProgressModel(0, CHUNK);
  const snap = model.snapshot();
  assertEqual(snap.chunkCount, 0, "chunkCount");
  assertEqual(snap.completed, true, "completed");
  assertEqual(snap.confirmedBytes, 0, "confirmedBytes");
  assertEqual(snap.transferredBytes, 0, "transferredBytes");
  const attempt = model.beginAttempt();
  model.reportProgress(attempt, 0, 10);
  model.confirmChunk(attempt, 0);
  assertEqual(model.snapshot().transferredBytes, 0, "events on invalid chunks are ignored");
});

test("accumulates out-of-order completion receipts up to exactly the total", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const attempt = model.beginAttempt();
  model.confirmChunk(attempt, 2);
  assertInvariant(model);
  assertEqual(model.snapshot().confirmedBytes, 256, "confirmed after chunk 2");
  assertEqual(model.snapshot().confirmedChunks, [2], "confirmedChunks");
  model.confirmChunk(attempt, 0);
  assertInvariant(model);
  model.confirmChunk(attempt, 3);
  assertInvariant(model);
  assertEqual(model.snapshot().confirmedBytes, 744, "confirmed after chunks 0, 2, 3");
  assertEqual(model.snapshot().completed, false, "not completed before last chunk");
  model.confirmChunk(attempt, 1);
  assertInvariant(model);
  const snap = model.snapshot();
  assertEqual(snap.confirmedBytes, TOTAL, "confirmed equals total");
  assertEqual(snap.transferredBytes, TOTAL, "transferred equals total");
  assertEqual(snap.completed, true, "completed");
  assertEqual(snap.confirmedChunks, [0, 1, 2, 3], "confirmedChunks sorted");
});

test("keeps only the maximum tentative report per chunk", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const attempt = model.beginAttempt();
  model.reportProgress(attempt, 1, 100);
  model.reportProgress(attempt, 1, 40);
  model.reportProgress(attempt, 1, 200);
  assertInvariant(model);
  assertEqual(model.snapshot().tentativeBytes, 200, "tentative keeps the maximum report");
});

test("clamps tentative reports to the chunk size", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const attempt = model.beginAttempt();
  model.reportProgress(attempt, 0, 999999);
  assertInvariant(model);
  assertEqual(model.snapshot().tentativeBytes, 256, "tentative clamped to chunk size");
  model.reportProgress(attempt, 3, 999999);
  assertInvariant(model);
  assertEqual(model.snapshot().tentativeBytes, 256 + 232, "last chunk clamped to its partial size");
});

test("ignores tentative reports for already confirmed chunks", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const attempt = model.beginAttempt();
  model.confirmChunk(attempt, 1);
  model.reportProgress(attempt, 1, 256);
  assertInvariant(model);
  assertEqual(model.snapshot().tentativeBytes, 0, "no tentative bytes on confirmed chunks");
  assertEqual(model.snapshot().transferredBytes, 256, "transferred reflects confirmed only");
});

test("treats duplicate completion receipts as idempotent", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const attempt = model.beginAttempt();
  model.confirmChunk(attempt, 1);
  model.confirmChunk(attempt, 1);
  model.confirmChunk(attempt, 1, 256);
  assertInvariant(model);
  assertEqual(model.snapshot().confirmedBytes, 256, "duplicate receipts are counted once");
  assertEqual(model.snapshot().confirmedChunks, [1], "chunk appears once");
});

test("clamps completion receipt bytes to the remaining chunk bytes", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const attempt = model.beginAttempt();
  model.confirmChunk(attempt, 3, 999999);
  assertInvariant(model);
  assertEqual(model.snapshot().confirmedBytes, 232, "receipt clamped to partial last chunk");
  model.confirmChunk(attempt, 0, 100);
  assertInvariant(model);
  assertEqual(model.snapshot().confirmedBytes, 332, "under-reported receipt is taken as-is");
});

test("retry rolls back tentative progress but keeps confirmed bytes", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const first = model.beginAttempt();
  model.confirmChunk(first, 0);
  model.reportProgress(first, 1, 200);
  model.reportProgress(first, 2, 128);
  assertInvariant(model);
  assertEqual(model.snapshot().tentativeBytes, 328, "tentative before retry");
  model.failAttempt(first);
  assertInvariant(model);
  assertEqual(model.snapshot().tentativeBytes, 0, "tentative rolled back on retry");
  assertEqual(model.snapshot().confirmedBytes, 256, "confirmed survives retry");
  assertEqual(model.snapshot().transferredBytes, 256, "transferred falls back to confirmed");
  const second = model.beginAttempt();
  model.reportProgress(second, 1, 200);
  assertEqual(model.snapshot().tentativeBytes, 200, "new attempt can report again");
  model.failAttempt(first);
  assertEqual(model.snapshot().tentativeBytes, 200, "failing a stale attempt is a no-op");
});

test("ignores events from stale attempts", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const first = model.beginAttempt();
  model.reportProgress(first, 0, 100);
  const second = model.beginAttempt();
  model.reportProgress(first, 1, 100);
  model.confirmChunk(first, 2);
  assertInvariant(model);
  assertEqual(model.snapshot().tentativeBytes, 0, "stale tentative reports ignored");
  assertEqual(model.snapshot().confirmedBytes, 0, "stale receipts ignored");
  assertEqual(model.snapshot().confirmedChunks, [], "no confirmed chunks from stale attempt");
  model.reportProgress(second, 1, 100);
  assertEqual(model.snapshot().tentativeBytes, 100, "current attempt reports accepted");
});

test("pause retains the confirmed set and clears tentative progress", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const first = model.beginAttempt();
  model.confirmChunk(first, 0);
  model.confirmChunk(first, 1);
  model.reportProgress(first, 2, 200);
  model.pause();
  const pausedSnap = model.snapshot();
  assertEqual(pausedSnap.paused, true, "paused flag");
  assertEqual(pausedSnap.confirmedBytes, 512, "confirmed set retained across pause");
  assertEqual(pausedSnap.confirmedChunks, [0, 1], "confirmed chunks retained across pause");
  assertEqual(pausedSnap.tentativeBytes, 0, "tentative cleared on pause");
  model.reportProgress(first, 2, 200);
  model.confirmChunk(first, 2);
  assertEqual(model.snapshot().confirmedBytes, 512, "events while paused are ignored");
  assertEqual(model.snapshot().tentativeBytes, 0, "no tentative progress while paused");
});

test("resume starts a new attempt that can finish the upload", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const first = model.beginAttempt();
  model.confirmChunk(first, 0);
  model.confirmChunk(first, 1);
  model.pause();
  const second = model.resume();
  if (second === first) {
    throw new Error("resume must start a new attempt");
  }
  assertEqual(model.snapshot().paused, false, "pause cleared after resume");
  model.confirmChunk(second, 2);
  model.confirmChunk(second, 3);
  assertInvariant(model);
  const snap = model.snapshot();
  assertEqual(snap.completed, true, "completed after resume finishes remaining chunks");
  assertEqual(snap.confirmedBytes, TOTAL, "confirmed equals total");
});

test("never exceeds total bytes across a mixed out-of-order flow", () => {
  const model = new UploadProgressModel(TOTAL, CHUNK);
  const first = model.beginAttempt();
  model.reportProgress(first, 0, 256);
  model.reportProgress(first, 1, 128);
  model.confirmChunk(first, 2);
  assertInvariant(model);
  model.failAttempt(first);
  assertInvariant(model);
  const second = model.beginAttempt();
  model.confirmChunk(second, 1, 256);
  model.reportProgress(second, 3, 232);
  assertInvariant(model);
  model.confirmChunk(second, 0);
  model.confirmChunk(second, 1, 256);
  model.reportProgress(second, 3, 232);
  assertInvariant(model);
  assertEqual(model.snapshot().confirmedBytes, 768, "duplicate receipt and stale report ignored");
  model.confirmChunk(second, 3);
  assertInvariant(model);
  const snap = model.snapshot();
  assertEqual(snap.completed, true, "completed");
  assertEqual(snap.confirmedBytes, TOTAL, "confirmed equals total exactly");
  assertEqual(snap.tentativeBytes, 0, "no tentative bytes remain");
  assertEqual(snap.transferredBytes, TOTAL, "transferred equals total exactly");
});

if (failures > 0) {
  throw new Error(failures + " test(s) failed");
}
console.log("all tests passed");
