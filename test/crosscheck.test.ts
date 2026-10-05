import assert from "node:assert/strict";
import test from "node:test";
import { BitemporalFileStore, ReferenceStore } from "../src/index.ts";
import type { QueryResult } from "../src/index.ts";

function makeRng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function normalize(result: QueryResult | null): unknown {
  if (result === null) return null;
  return {
    fileId: result.fileId,
    attributes: result.attributes,
    sourceRevisionId: result.source.revisionId,
    sourceRecordedAt: result.source.recordedAt,
  };
}

function runCrossCheck(seed: number, steps: number): void {
  const rng = makeRng(seed);
  const indexed = new BitemporalFileStore();
  const reference = new ReferenceStore();
  const fileIds = ["alpha", "beta", "gamma"];
  const liveRevisionIds: number[] = [];
  const pick = (items: number[]): number => {
    const item = items[Math.floor(rng() * items.length)];
    if (item === undefined) throw new Error("empty pick");
    return item;
  };
  const pickFile = (): string => {
    const item = fileIds[Math.floor(rng() * fileIds.length)];
    if (item === undefined) throw new Error("empty pick");
    return item;
  };

  const checkQueries = (label: string): void => {
    const version = indexed.currentVersion;
    for (let i = 0; i < 12; i++) {
      const fileId = pickFile();
      const validTime = Math.floor(rng() * 60) - 5;
      const knownAt = Math.floor(rng() * (version + 2));
      const expected = reference.query(fileId, validTime, knownAt);
      const actual = indexed.query(fileId, validTime, knownAt);
      assert.deepStrictEqual(
        normalize(actual),
        normalize(expected),
        label + ": query(" + fileId + ", valid=" + validTime + ", known=" + knownAt + ")",
      );
    }
  };

  for (let step = 0; step < steps; step++) {
    const roll = rng();
    if (roll < 0.7 || liveRevisionIds.length === 0) {
      const backfill = rng() < 0.4;
      const base = backfill
        ? Math.floor(rng() * 30)
        : 20 + Math.floor(rng() * 30);
      const length = 1 + Math.floor(rng() * 10);
      const input = {
        fileId: pickFile(),
        attributes: { step, marker: Math.floor(rng() * 1000) },
        validFrom: base,
        validTo: base + length,
      };
      const revision = indexed.record(input);
      reference.record(input);
      liveRevisionIds.push(revision.revisionId);
    } else {
      const id = rng() < 0.5
        ? liveRevisionIds[liveRevisionIds.length - 1]
        : pick(liveRevisionIds);
      if (id === undefined) continue;
      indexed.revoke(id);
      reference.revoke(id);
      liveRevisionIds.splice(liveRevisionIds.indexOf(id), 1);
    }
    assert.equal(indexed.currentVersion, reference.currentVersion);
    checkQueries("seed=" + seed + " step=" + step);
  }
}

test("indexed store matches scan reference under random workloads", () => {
  for (const seed of [1, 7, 42, 1337, 20261005]) {
    runCrossCheck(seed, 250);
  }
});
