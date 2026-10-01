import { test } from "node:test";
import assert from "node:assert/strict";
import {
  computeChecksum,
  ErrorIndex,
  FORMAT,
  measureSpaceSavings,
  type SerializedIndex,
} from "../src/error-index.ts";
import { toMultiset, type ValidationError } from "../src/errors.ts";
import { validate, type Schema } from "../src/validator.ts";

function sampleErrors(): ValidationError[] {
  const schema: Schema = {
    type: "object",
    properties: {
      items: {
        type: "array",
        items: {
          type: "object",
          properties: { qty: { type: "integer", minimum: 1 } },
        },
      },
    },
  };
  const data = { items: Array.from({ length: 100 }, () => ({ qty: 0 })) };
  return validate(schema, data, { schemaVersion: "v1" });
}

function resign(payload: Omit<SerializedIndex, "checksum">): SerializedIndex {
  const { format, totalCount, groups } = payload as SerializedIndex;
  const clean = { format, totalCount, groups };
  return { ...clean, checksum: computeChecksum(clean) };
}

test("serialize/deserialize round trip preserves the error multiset", () => {
  const errors = sampleErrors();
  const index = ErrorIndex.fromErrors(errors);
  const restored = ErrorIndex.deserialize(index.toJSONString());
  assert.deepEqual(toMultiset(restored.expand()), toMultiset(errors));
  assert.equal(restored.totalCount, 100);
});

test("tampering with a count is detected via checksum", () => {
  const index = ErrorIndex.fromErrors(sampleErrors());
  const payload = index.serialize();
  const forged: SerializedIndex = JSON.parse(JSON.stringify(payload));
  forged.totalCount = 1; // fake the summary count
  assert.throws(() => ErrorIndex.deserialize(forged), /checksum mismatch/);
});

test("tampering with a range is detected via checksum", () => {
  const index = ErrorIndex.fromErrors(sampleErrors());
  const forged: SerializedIndex = JSON.parse(JSON.stringify(index.serialize()));
  forged.groups[0].ranges = [[0, 10000, 1]]; // fake a huge continuous range
  assert.throws(() => ErrorIndex.deserialize(forged), /checksum mismatch/);
});

test("illegal ranges are rejected even with a valid (recomputed) checksum", () => {
  const index = ErrorIndex.fromErrors(sampleErrors());
  const good = index.serialize();

  const reversed = JSON.parse(JSON.stringify(good));
  reversed.groups[0].ranges = [[50, 10, 1]]; // start > end
  reversed.groups[0].count = 0;
  reversed.totalCount = 0;
  assert.throws(() => ErrorIndex.deserialize(resign(reversed)), /start 50 > end 10/);

  const zeroMult = JSON.parse(JSON.stringify(good));
  zeroMult.groups[0].ranges = [[0, 99, 0]];
  zeroMult.groups[0].count = 0;
  zeroMult.totalCount = 0;
  assert.throws(() => ErrorIndex.deserialize(resign(zeroMult)), /mult must be >= 1/);

  const overlapping = JSON.parse(JSON.stringify(good));
  overlapping.groups[0].ranges = [
    [0, 60, 1],
    [50, 99, 1],
  ];
  assert.throws(() => ErrorIndex.deserialize(resign(overlapping)), /overlapping|unsorted/);

  const unmerged = JSON.parse(JSON.stringify(good));
  unmerged.groups[0].ranges = [
    [0, 49, 1],
    [50, 99, 1],
  ];
  assert.throws(
    () => ErrorIndex.deserialize(resign(unmerged)),
    /adjacent ranges with equal mult must be merged/,
  );
});

test("forged group count that disagrees with ranges is rejected", () => {
  const index = ErrorIndex.fromErrors(sampleErrors());
  const forged = JSON.parse(JSON.stringify(index.serialize()));
  forged.groups[0].count = 99; // ranges still say 100
  forged.totalCount = 99;
  assert.throws(
    () => ErrorIndex.deserialize(resign(forged)),
    /count 99 does not match ranges \(100\)/,
  );
});

test("totalCount that disagrees with the sum of groups is rejected", () => {
  const index = ErrorIndex.fromErrors(sampleErrors());
  const forged = JSON.parse(JSON.stringify(index.serialize()));
  forged.totalCount = 101;
  assert.throws(
    () => ErrorIndex.deserialize(resign(forged)),
    /count mismatch/,
  );
});

test("unsupported format version is rejected", () => {
  const index = ErrorIndex.fromErrors(sampleErrors());
  const payload = JSON.parse(JSON.stringify(index.serialize()));
  payload.format = "compressed-error-index/999";
  payload.checksum = computeChecksum({ ...payload, checksum: undefined });
  assert.throws(() => ErrorIndex.deserialize(payload), /unsupported format/);
  assert.equal(FORMAT, "compressed-error-index/1");
});

test("compressed form is dramatically smaller than the raw error list", () => {
  const errors = sampleErrors();
  const index = ErrorIndex.fromErrors(errors);
  const savings = measureSpaceSavings(errors, index);
  assert.ok(savings.compressedBytes < savings.rawBytes / 5);
  assert.equal(savings.savedBytes, savings.rawBytes - savings.compressedBytes);
});
