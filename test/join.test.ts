import assert from "node:assert/strict";
import test from "node:test";
import { assessCrossDatasetJoin, assessReidentification } from "../src/index.ts";
import type { Dataset } from "../src/types.ts";

const target: Dataset = {
  name: "published",
  records: [
    { id: "t1", age: 30, zip: "10001" },
    { id: "t2", age: 31, zip: "10002" },
    { id: "t3", age: 31, zip: "10002" },
    { id: "t4", age: 32, zip: "10003" },
    { id: "t5", age: 33, zip: "10004" },
  ],
};

const auxiliary: Dataset = {
  name: "registry",
  records: [
    { personId: "a1", age: 30, zip: "10001" },
    { personId: "a1", age: 30, zip: "10001" },
    { personId: "a2", age: 31, zip: "10002" },
    { personId: "a3", age: 32, zip: "10003" },
    { personId: "a4", age: 32, zip: "10003" },
  ],
};

test("cross-dataset join distinguishes identity and row uniquification", () => {
  const result = assessCrossDatasetJoin(target, auxiliary, {
    qis: ["age", "zip"],
    auxiliaryRecordIdField: "personId",
    threshold: 2,
  });

  assert.equal(result.targetRecordCount, 5);
  assert.equal(result.auxiliaryRecordCount, 5);
  assert.equal(result.auxiliaryIdentityCount, 4);
  assert.equal(result.matchedTargetRecordCount, 4);
  assert.equal(result.unmatchedTargetRecordCount, 1);
  assert.equal(result.uniqueAuxiliaryCandidateCount, 3);
  assert.equal(result.uniquelyReidentifiedRecordCount, 1);
  assert.equal(result.narrowedToSingleIdentityCount, 2);
  assert.equal(result.ambiguousTargetRecordCount, 1);
  assert.equal(result.baselineUniqueTargetRecordCount, 3);
  assert.equal(result.riskLevel, "high");
  assert.equal(result.maxCandidateCount, 2);
  assert.equal(result.minCandidateCount, 1);
  assert.equal(result.medianCandidateCount, 1);

  const t1 = result.matches.find((match) => match.targetRecordId === "t1");
  assert.deepEqual(t1?.auxiliaryIdentities, ["a1"]);
  assert.equal(t1?.uniqueAuxiliaryCandidate, true);
  assert.equal(t1?.uniquelyReidentified, true);
  assert.equal(t1?.candidatePairs, 1);

  const t2 = result.matches.find((match) => match.targetRecordId === "t2");
  assert.equal(t2?.targetClassSize, 2);
  assert.equal(t2?.uniqueAuxiliaryCandidate, true);
  assert.equal(t2?.uniquelyReidentified, false);
  assert.equal(t2?.narrowedToSingleIdentity, true);
  assert.equal(t2?.candidatePairs, 2);

  const t4 = result.matches.find((match) => match.targetRecordId === "t4");
  assert.equal(t4?.targetClassSize, 1);
  assert.equal(t4?.auxiliaryCandidateCount, 2);
  assert.equal(t4?.uniquelyReidentified, false);

  const t5 = result.matches.find((match) => match.targetRecordId === "t5");
  assert.equal(t5?.matched, false);
  assert.equal(t5?.candidatePairs, 0);
});

test("facade reports join risk alongside k-anonymity", () => {
  const result = assessReidentification({
    dataset: target,
    quasiIdentifierCombinations: [["age", "zip"]],
    threshold: 2,
    auxiliary: {
      dataset: auxiliary,
      recordIdField: "personId",
    },
  });

  assert.equal(result.joins.length, 1);
  assert.equal(result.joins[0]?.uniquelyReidentifiedRecordCount, 1);
  assert.equal(result.overallFlagged, true);
});
