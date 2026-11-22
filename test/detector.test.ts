import { test } from "node:test";
import assert from "node:assert/strict";
import { BehaviorDetector, type WriteEvent } from "../src/detector.ts";
import { shannonEntropy } from "../src/entropy.ts";

const T0 = 1_700_000_000_000;

function makeEvent(index: number, overrides: Partial<WriteEvent> = {}): WriteEvent {
  return {
    path: `/data/share/file-${index}.docx`,
    volume: "local",
    timestamp: T0 + index,
    previousExtension: ".docx",
    extension: ".docx",
    previousEntropy: 4.2,
    entropy: 4.3,
    ...overrides,
  };
}

test("normal job 1: batch compression backup is excluded", () => {
  const detector = new BehaviorDetector();
  for (let i = 0; i < 60; i++) {
    detector.ingest(
      makeEvent(i, {
        path: `/backup/docs/report-${i}.zip`,
        previousExtension: ".docx",
        extension: ".zip",
        entropy: 7.8,
        processName: "backup-agent",
      }),
    );
  }
  const verdict = detector.evaluate(T0 + 5_000);
  assert.equal(verdict.rewriteCount, 60);
  assert.equal(verdict.extChangeCount, 0);
  assert.equal(verdict.entropyJumpCount, 0);
  assert.equal(verdict.alert, false);
  assert.deepEqual(verdict.reasons, [
    `rewrite rate 60/20 in 10000ms`,
  ]);
});

test("normal job 2: video transcode, duplicates counted once, snapshot ignored", () => {
  const detector = new BehaviorDetector();
  for (let i = 0; i < 40; i++) {
    detector.ingest(
      makeEvent(i, {
        path: `/media/clip-${i}.mp4`,
        volume: "shared",
        previousExtension: ".mov",
        extension: ".mp4",
        previousEntropy: 5.1,
        entropy: 7.6,
        processName: "ffmpeg",
      }),
    );
  }
  // Same files rewritten again inside the window: still one count each.
  for (let i = 0; i < 40; i++) {
    detector.ingest(
      makeEvent(i, {
        path: `/media/clip-${i}.mp4`,
        volume: "shared",
        timestamp: T0 + 1_000 + i,
        previousExtension: ".mov",
        extension: ".mp4",
        previousEntropy: 5.1,
        entropy: 7.7,
      }),
    );
  }
  // Snapshot volume writes must not enter statistics.
  for (let i = 0; i < 100; i++) {
    detector.ingest(
      makeEvent(i, {
        path: `/media/clip-${i}.mp4`,
        volume: "snapshot",
        timestamp: T0 + 2_000 + i,
        extension: ".locked",
        entropy: 7.99,
      }),
    );
  }
  const verdict = detector.evaluate(T0 + 5_000);
  assert.equal(verdict.rewriteCount, 40);
  assert.equal(verdict.extChangeCount, 0);
  assert.equal(verdict.entropyJumpCount, 0);
  assert.equal(verdict.alert, false);
});

test("encryption rewrite triggers joint alert with counts and thresholds", () => {
  const detector = new BehaviorDetector();
  for (let i = 0; i < 50; i++) {
    detector.ingest(
      makeEvent(i, {
        path: `/data/share/file-${i}.docx`,
        timestamp: T0 + i * 10,
        extension: ".lockbit",
        entropy: 7.9,
      }),
    );
  }
  // Duplicate rewrites of already-seen files must not inflate counts.
  for (let i = 0; i < 50; i++) {
    detector.ingest(
      makeEvent(i, {
        path: `/data/share/file-${i}.docx`,
        timestamp: T0 + 600 + i * 10,
        extension: ".lockbit",
        entropy: 7.95,
      }),
    );
  }
  // Snapshot copies of the same attack are ignored.
  for (let i = 0; i < 50; i++) {
    detector.ingest(
      makeEvent(i, {
        path: `/data/share/file-${i}.docx`,
        volume: "snapshot",
        timestamp: T0 + 900 + i,
        extension: ".lockbit",
        entropy: 7.99,
      }),
    );
  }
  const verdict = detector.evaluate(T0 + 5_000);
  assert.equal(verdict.rewriteCount, 50);
  assert.equal(verdict.extChangeCount, 50);
  assert.equal(verdict.entropyJumpCount, 50);
  assert.deepEqual(verdict.thresholds, { rewrite: 20, extChange: 10, entropyJump: 10 });
  assert.equal(verdict.alert, true);
  assert.equal(verdict.reasons.length, 3);
});

test("window slides: old events expire", () => {
  const detector = new BehaviorDetector({ windowMs: 1_000 });
  for (let i = 0; i < 30; i++) {
    detector.ingest(makeEvent(i, { timestamp: T0 + i, extension: ".enc", entropy: 7.9 }));
  }
  const verdict = detector.evaluate(T0 + 5_000);
  assert.equal(verdict.rewriteCount, 0);
  assert.equal(verdict.alert, false);
});

test("shannonEntropy distinguishes text from random data", () => {
  const text = new TextEncoder().encode("hello world ".repeat(100));
  const random = crypto.getRandomValues(new Uint8Array(4096));
  const low = shannonEntropy(text);
  const high = shannonEntropy(random);
  assert.ok(low < 4.5, `text entropy ${low}`);
  assert.ok(high > 7.5, `random entropy ${high}`);
});
