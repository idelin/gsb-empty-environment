import assert from "node:assert/strict";
import test from "node:test";
import {
  assessQuasiIdentifierCombinations,
  assessQuasiIdentifiers,
  assessReidentification,
} from "../src/index.ts";
import type { Dataset } from "../src/types.ts";

const dataset: Dataset = {
  name: "single-release",
  records: [
    { id: "p1", age: 30, zip: "10001", gender: "F" },
    { id: "p2", age: 30, zip: "10001", gender: "F" },
    { id: "p3", age: 31, zip: "10002", gender: "F" },
    { id: "p4", age: 31, zip: "10002", gender: "M" },
    { id: "p5", age: 32, zip: "10003", gender: "M" },
  ],
};

test("single release: equivalence class sizes and threshold flags", () => {
  const result = assessQuasiIdentifiers(dataset, ["age", "zip"], { threshold: 2 });

  assert.equal(result.recordCount, 5);
  assert.equal(result.equivalenceClassCount, 3);
  assert.deepEqual(result.classSizes, [1, 2, 2]);
  assert.equal(result.uniqueClassCount, 1);
  assert.equal(result.uniqueRecordCount, 1);
  assert.equal(result.belowThresholdClassCount, 1);
  assert.equal(result.belowThresholdRecordCount, 1);
  assert.equal(result.minClassSize, 1);
  assert.equal(result.medianClassSize, 2);
  assert.equal(result.maxClassSize, 2);
  assert.equal(result.maxReidentificationProbability, 1);
  assert.equal(result.flagged, true);

  const [smallest] = result.classes;
  assert.equal(smallest?.size, 1);
  assert.deepEqual(smallest?.recordIds, ["p5"]);
  assert.equal(smallest?.belowThreshold, true);
  assert.equal(smallest?.unique, true);
  assert.equal(smallest?.reidentificationProbability, 1);
});

test("single release: a broad combination can remain unflagged", () => {
  const report = assessQuasiIdentifierCombinations(
    dataset,
    [["age", "zip"], ["gender"]],
    { threshold: 2 },
  );
  const gender = report.combinations[1];

  assert.equal(report.overallFlagged, true);
  assert.deepEqual(report.flaggedCombinations.map((assessment) => assessment.qis), [
    ["age", "zip"],
  ]);
  assert.equal(gender?.flagged, false);
  assert.deepEqual(gender?.classSizes, [2, 3]);
  assert.equal(gender?.maxReidentificationProbability, 0.5);
});

test("canonical values keep numbers and numeric strings distinct", () => {
  const typedDataset: Dataset = {
    records: [
      { id: "a", value: 30 },
      { id: "b", value: "30" },
    ],
  };
  const result = assessQuasiIdentifiers(typedDataset, ["value"], { threshold: 2 });

  assert.equal(result.equivalenceClassCount, 2);
  assert.equal(result.uniqueClassCount, 2);
  assert.equal(result.flagged, true);
});

test("missing-value policy is explicit", () => {
  const missingDataset: Dataset = {
    records: [
      { id: "a", age: 30, zip: null },
      { id: "b", age: 30, zip: "10001" },
      { id: "c", age: 30, zip: undefined },
    ],
  };

  const asValue = assessQuasiIdentifiers(missingDataset, ["age", "zip"], {
    threshold: 2,
    missingValuePolicy: "as-value",
  });
  const excluded = assessQuasiIdentifiers(missingDataset, ["age", "zip"], {
    threshold: 2,
    missingValuePolicy: "exclude",
  });

  assert.equal(asValue.recordCount, 3);
  assert.equal(asValue.equivalenceClassCount, 2);
  assert.equal(excluded.recordCount, 1);
  assert.equal(excluded.excludedRecordCount, 2);
});

test("facade evaluates each combination and exposes flagged ones", () => {
  const result = assessReidentification({
    dataset,
    quasiIdentifierCombinations: [["age", "zip"], ["gender"]],
    threshold: 2,
  });

  assert.equal(result.threshold, 2);
  assert.equal(result.combinations.length, 2);
  assert.equal(result.flaggedCombinations.length, 1);
  assert.equal(result.overallFlagged, true);
  assert.deepEqual(result.joins, []);
});
