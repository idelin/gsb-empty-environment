import assert from "node:assert/strict";
import test from "node:test";
import {
  assessCumulativeReleases,
  planSingleReleaseGeneralization,
} from "../src/index.ts";
import type { Dataset, GeneralizationHierarchy, Release } from "../src/types.ts";

const intersectionReleases: Release[] = [
  {
    id: "r1",
    name: "broad-release",
    qis: ["region", "ageBand"],
    records: [
      { id: "A", region: "North", ageBand: "20-29" },
      { id: "B", region: "North", ageBand: "20-29" },
      { id: "C", region: "South", ageBand: "20-29" },
      { id: "D", region: "South", ageBand: "20-29" },
    ],
  },
  {
    id: "r2",
    name: "orthogonal-release",
    qis: ["zone", "zip3"],
    records: [
      { id: "A", zone: "North", zip3: "100" },
      { id: "B", zone: "South", zip3: "200" },
      { id: "C", zone: "North", zip3: "100" },
      { id: "D", zone: "South", zip3: "200" },
    ],
  },
];

test("multiple releases: each release is safe but their intersection is unique", () => {
  const result = assessCumulativeReleases(intersectionReleases, { threshold: 2 });

  assert.equal(result.releaseCount, 2);
  assert.equal(result.releases[0]?.assessment.flagged, false);
  assert.equal(result.releases[1]?.assessment.flagged, false);
  assert.equal(result.releases[0]?.assessment.minClassSize, 2);
  assert.equal(result.releases[1]?.assessment.minClassSize, 2);

  assert.equal(result.cumulativeDistinctRecordCount, 4);
  assert.equal(result.totalReleasedRecordCount, 8);
  assert.equal(result.overlapRecordCount, 4);
  assert.equal(result.averageReleasesPerIndividual, 2);
  assert.equal(result.equivalenceClassCount, 4);
  assert.deepEqual(result.classSizes, [1, 1, 1, 1]);
  assert.equal(result.uniqueBeforeCount, 0);
  assert.equal(result.uniqueAfterCount, 4);
  assert.equal(result.newlyUniqueRecordCount, 4);
  assert.equal(result.amplifiedRecordCount, 4);
  assert.deepEqual(result.newlyUniqueRecordIds.sort(), ["A", "B", "C", "D"]);
  assert.equal(result.flagged, true);
  assert.equal(result.conflicts.length, 0);

  const a = result.individuals.find((individual) => individual.recordId === "A");
  assert.equal(a?.baselineMinimumClassSize, 2);
  assert.equal(a?.classSize, 1);
  assert.equal(a?.uniqueBefore, false);
  assert.equal(a?.uniqueAfter, true);
  assert.equal(a?.amplifiedByIntersection, true);
});

test("cumulative recommendation proposes corrective generalization for published copies", () => {
  const regionHierarchy: GeneralizationHierarchy = {
    column: "region",
    levels: [
      { label: "original", transform: (value) => value },
      { label: "region suppressed", transform: () => "*" },
    ],
  };
  const result = assessCumulativeReleases(intersectionReleases, {
    threshold: 2,
    generalizationHierarchies: [regionHierarchy],
  });
  const recommendation = result.recommendation;

  assert.equal(recommendation.required, true);
  assert.equal(recommendation.urgency, "corrective");
  assert.equal(recommendation.scope, "supplemental-generalized-release");
  assert.equal(recommendation.achievable, true);
  assert.deepEqual(
    recommendation.actions.map((action) => [action.column, action.kind]),
    [["region", "generalize"]],
  );
  assert.deepEqual(recommendation.actions[0]?.releaseIds, ["r1"]);
  assert.equal(recommendation.projected.flagged, false);
  assert.equal(recommendation.projected.minClassSize, 2);
  assert.equal(recommendation.projected.belowThresholdRecordCount, 0);
  assert.match(recommendation.rationale, /4 individual\(s\) became unique/);
  assert.match(recommendation.notes.join(" "), /supplemental generalized release/);
  assert.deepEqual(recommendation.before.classSizes, result.classSizes);
});

test("single-release generalization projects a threshold-safe plan", () => {
  const dataset: Dataset = {
    records: [
      { id: "A", age: 30 },
      { id: "B", age: 31 },
    ],
  };
  const ageHierarchy: GeneralizationHierarchy = {
    column: "age",
    levels: [
      { label: "original", transform: (value) => value },
      { label: "decade", transform: (value) => `${Math.floor(Number(value) / 10) * 10}s` },
    ],
  };
  const plan = planSingleReleaseGeneralization(dataset, ["age"], {
    threshold: 2,
    hierarchies: [ageHierarchy],
  });

  assert.equal(plan.before.flagged, true);
  assert.equal(plan.achievable, true);
  assert.deepEqual(
    plan.actions.map((action) => [action.column, action.level, action.kind]),
    [["age", 1, "generalize"]],
  );
  assert.equal(plan.projected.minClassSize, 2);
  assert.equal(plan.projected.flagged, false);
});

test("differential release: disjoint records do not create artificial intersection uniqueness", () => {
  const disjoint: Release[] = [
    {
      id: "batch-1",
      qis: ["region"],
      records: [
        { id: "A", region: "North" },
        { id: "B", region: "North" },
        { id: "C", region: "South" },
        { id: "D", region: "South" },
      ],
    },
    {
      id: "batch-2",
      qis: ["region"],
      records: [
        { id: "E", region: "North" },
        { id: "F", region: "North" },
        { id: "G", region: "South" },
        { id: "H", region: "South" },
      ],
    },
  ];
  const result = assessCumulativeReleases(disjoint, { threshold: 2 });

  assert.equal(result.totalReleasedRecordCount, 8);
  assert.equal(result.cumulativeDistinctRecordCount, 8);
  assert.equal(result.overlapRecordCount, 0);
  assert.equal(result.averageReleasesPerIndividual, 1);
  assert.deepEqual(result.classSizes, [2, 2, 2, 2]);
  assert.equal(result.flagged, false);
  assert.equal(result.newlyUniqueRecordCount, 0);
  assert.equal(result.amplifiedRecordCount, 0);
  assert.equal(result.recommendation.required, false);
  assert.equal(result.recommendation.urgency, "none");
  assert.deepEqual(
    result.releaseDeltas.map((delta) => [
      delta.releaseId,
      delta.addedRecordCount,
      delta.retainedRecordCount,
      delta.droppedRecordCount,
    ]),
    [
      ["batch-1", 4, 0, 0],
      ["batch-2", 4, 0, 4],
    ],
  );
});

test("differential release: retained and dropped records are reported without false amplification", () => {
  const subset: Release[] = [
    {
      id: "full",
      qis: ["region"],
      records: [
        { id: "A", region: "North" },
        { id: "B", region: "North" },
        { id: "C", region: "South" },
        { id: "D", region: "South" },
      ],
    },
    {
      id: "delta",
      qis: ["region"],
      records: [
        { id: "A", region: "North" },
        { id: "B", region: "North" },
      ],
    },
  ];
  const result = assessCumulativeReleases(subset, { threshold: 2 });

  assert.equal(result.totalReleasedRecordCount, 6);
  assert.equal(result.cumulativeDistinctRecordCount, 4);
  assert.equal(result.overlapRecordCount, 2);
  assert.equal(result.averageReleasesPerIndividual, 1.5);
  assert.equal(result.flagged, false);
  assert.equal(result.newlyUniqueRecordCount, 0);
  assert.equal(result.amplifiedRecordCount, 0);
  assert.deepEqual(result.releaseDeltas[1], {
    releaseId: "delta",
    addedRecordCount: 0,
    retainedRecordCount: 2,
    droppedRecordCount: 2,
    addedRecordIds: [],
    retainedRecordIds: ["A", "B"],
    droppedRecordIds: ["C", "D"],
  });
});

test("conflicting repeated values can be rejected", () => {
  const conflicting: Release[] = [
    {
      id: "v1",
      qis: ["age"],
      records: [{ id: "A", age: 30 }],
    },
    {
      id: "v2",
      qis: ["age"],
      records: [{ id: "A", age: 31 }],
    },
  ];

  assert.throws(
    () => assessCumulativeReleases(conflicting, { threshold: 1, conflictPolicy: "reject" }),
    /conflicting values/,
  );

  const flagged = assessCumulativeReleases(conflicting, { threshold: 1 });
  assert.equal(flagged.conflicts.length, 1);
  assert.equal(flagged.individuals[0]?.conflictingColumns[0], "age");
});
