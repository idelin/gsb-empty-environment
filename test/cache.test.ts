import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runValidation, type Sample } from "../src/diff.ts";
import { ResultCache } from "../src/cache.ts";

function makeCacheDir(): string {
  return mkdtempSync(join(tmpdir(), "schemadiff-"));
}

const schema = { type: "object", required: ["age"], properties: { age: { type: "integer", minimum: 0 } } };

test("second run with identical schema and samples is a cache hit", () => {
  const dir = makeCacheDir();
  const cache = new ResultCache(join(dir, "cache"));
  const samples: Sample[] = [{ id: "a", data: { age: 1 } }];

  const first = runValidation(schema, samples, cache);
  assert.equal(first.cacheHit, false);
  assert.equal(first.payload.verdicts[0].valid, true);

  const second = runValidation(schema, samples, cache);
  assert.equal(second.cacheHit, true);
  assert.deepEqual(second.payload, first.payload);
});

test("changing a sample invalidates the cache; old pass is not reused", () => {
  const dir = makeCacheDir();
  const cache = new ResultCache(join(dir, "cache"));

  const goodSamples: Sample[] = [{ id: "a", data: { age: 1 } }];
  const first = runValidation(schema, goodSamples, cache);
  assert.equal(first.cacheHit, false);
  assert.equal(first.payload.verdicts[0].valid, true);

  const changedSamples: Sample[] = [{ id: "a", data: { age: -5 } }];
  const second = runValidation(schema, changedSamples, cache);
  assert.equal(second.cacheHit, false, "edited samples must cause a cache miss");
  assert.equal(second.payload.verdicts[0].valid, false);
  assert.equal(second.payload.verdicts[0].errors[0].keyword, "minimum");
});

test("schema change (same samples) invalidates the cache", () => {
  const dir = makeCacheDir();
  const cache = new ResultCache(join(dir, "cache"));
  const samples: Sample[] = [{ id: "a", data: { age: 1 } }];

  runValidation(schema, samples, cache);
  const stricter = { ...schema, required: ["age", "name"] };
  const second = runValidation(stricter, samples, cache);
  assert.equal(second.cacheHit, false);
  assert.equal(second.payload.verdicts[0].valid, false);
  assert.equal(second.payload.verdicts[0].errors[0].keyword, "required");
});

test("tampered cache entry with mismatched digest is rejected", () => {
  const cacheDir = join(makeCacheDir(), "cache");
  const cache = new ResultCache(cacheDir);
  const samples: Sample[] = [{ id: "a", data: { age: 1 } }];

  runValidation(schema, samples, cache);
  const files = readdirSync(cacheDir);
  const entryPath = join(cacheDir, files[0]);
  const tampered = JSON.parse(readFileSync(entryPath, "utf8"));
  tampered.schemaDigest = "deadbeef";
  writeFileSync(entryPath, JSON.stringify(tampered));

  assert.equal(cache.get(schema, samples), null);
});
