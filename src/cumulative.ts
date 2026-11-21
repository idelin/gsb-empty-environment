import {
  assertNonEmptyUniqueColumns,
  assertThreshold,
  canonicalizeQiValue,
  isMissing,
  valuesForColumns,
} from "./canonical.ts";
import { assessQuasiIdentifiers, summarizeClassSizes } from "./equivalence.ts";
import { recommendCumulativeGeneralization } from "./generalize.ts";
import type {
  CumulativeAssessment,
  CumulativeAssessmentOptions,
  CumulativeConflict,
  CumulativeEquivalenceClass,
  CumulativeIndividual,
  CumulativeReleaseAssessment,
  Release,
  ReleaseDelta,
} from "./types.ts";

interface MutableIndividual {
  readonly recordId: string;
  readonly presentReleaseIds: string[];
  readonly releaseValues: Record<string, Readonly<Record<string, unknown>>>;
}

function validateReleases(releases: readonly Release[]): void {
  if (releases.length === 0) {
    throw new Error("at least one release is required");
  }
  const releaseIds = new Set<string>();
  for (const release of releases) {
    if (typeof release.id !== "string" || release.id.length === 0) {
      throw new Error("every release must have a non-empty id");
    }
    if (releaseIds.has(release.id)) {
      throw new Error(`duplicate release id "${release.id}"`);
    }
    releaseIds.add(release.id);
    assertNonEmptyUniqueColumns(release.qis, `release "${release.id}" qis`);
  }
}

function cumulativeSignature(
  individual: MutableIndividual,
  releaseIds: readonly string[],
  releaseQis: ReadonlyMap<string, readonly string[]>,
): string {
  const parts: unknown[] = [];
  const present = new Set(individual.presentReleaseIds);
  for (const releaseId of releaseIds) {
    if (!present.has(releaseId)) {
      parts.push(["not-released", releaseId]);
      continue;
    }
    const qis = releaseQis.get(releaseId) ?? [];
    const values = individual.releaseValues[releaseId] ?? {};
    parts.push([
      "released",
      releaseId,
      ...qis.map((column) => [column, canonicalizeQiValue(values[column])]),
    ]);
  }
  return JSON.stringify(parts);
}

function detectConflicts(
  individual: MutableIndividual,
  conflictPolicy: "flag" | "reject",
): { readonly conflicts: CumulativeConflict[]; readonly columns: string[] } {
  const valuesByColumn = new Map<string, Record<string, unknown>>();
  for (const releaseId of individual.presentReleaseIds) {
    const values = individual.releaseValues[releaseId] ?? {};
    for (const [column, value] of Object.entries(values)) {
      const existing: Record<string, unknown> = valuesByColumn.get(column) ?? {};
      existing[releaseId] = value;
      valuesByColumn.set(column, existing);
    }
  }

  const conflicts: CumulativeConflict[] = [];
  for (const [column, releaseValues] of valuesByColumn) {
    const distinct = new Set(Object.values(releaseValues).map((value) => canonicalizeQiValue(value)));
    if (distinct.size <= 1) {
      continue;
    }
    const conflict = { recordId: individual.recordId, column, releaseValues };
    if (conflictPolicy === "reject") {
      throw new Error(
        `conflicting values for record "${individual.recordId}" column "${column}" across releases`,
      );
    }
    conflicts.push(conflict);
  }
  return { conflicts, columns: conflicts.map((conflict) => conflict.column) };
}

export function assessCumulativeReleases(
  releases: readonly Release[],
  options: CumulativeAssessmentOptions,
): CumulativeAssessment {
  assertThreshold(options.threshold);
  validateReleases(releases);
  const conflictPolicy = options.conflictPolicy ?? "flag";
  const releaseIds = releases.map((release) => release.id);
  const releaseQis = new Map<string, readonly string[]>();
  const individuals = new Map<string, MutableIndividual>();
  const releaseAssessments: CumulativeReleaseAssessment[] = [];
  const releaseClassSizes = new Map<string, Map<string, number>>();

  for (const release of releases) {
    assertNonEmptyUniqueColumns(release.qis, `release "${release.id}" qis`);
    releaseQis.set(release.id, release.qis);
    const recordIdField = release.recordIdField ?? options.recordIdField ?? "id";
    const seenRecordIds = new Set<string>();

    release.records.forEach((record, index) => {
      const rawRecordId = record[recordIdField];
      if (isMissing(rawRecordId)) {
        throw new Error(
          `release "${release.id}" record ${index} is missing linkage field "${recordIdField}"`,
        );
      }
      const recordId = String(rawRecordId);
      if (seenRecordIds.has(recordId)) {
        throw new Error(`release "${release.id}" contains duplicate record id "${recordId}"`);
      }
      seenRecordIds.add(recordId);

      const values = valuesForColumns(record, release.qis);
      const existing = individuals.get(recordId);
      if (existing === undefined) {
        individuals.set(recordId, {
          recordId,
          presentReleaseIds: [release.id],
          releaseValues: { [release.id]: values },
        });
      } else {
        existing.presentReleaseIds.push(release.id);
        existing.releaseValues[release.id] = values;
      }
    });

    const assessment = assessQuasiIdentifiers(
      {
        name: release.name ?? release.id,
        recordIdField,
        records: release.records,
      },
      release.qis,
      {
        threshold: options.threshold,
        recordIdField,
      },
    );
    releaseAssessments.push({
      releaseId: release.id,
      qis: [...release.qis],
      recordCount: release.records.length,
      assessment,
    });

    const classSizeByRecordId = new Map<string, number>();
    for (const equivalenceClass of assessment.classes) {
      for (const recordId of equivalenceClass.recordIds) {
        classSizeByRecordId.set(recordId, equivalenceClass.size);
      }
    }
    releaseClassSizes.set(release.id, classSizeByRecordId);
  }

  const groupMembers = new Map<string, string[]>();
  const individualSignatures = new Map<string, string>();
  for (const individual of individuals.values()) {
    const signature = cumulativeSignature(individual, releaseIds, releaseQis);
    individualSignatures.set(individual.recordId, signature);
    const members = groupMembers.get(signature);
    if (members === undefined) {
      groupMembers.set(signature, [individual.recordId]);
    } else {
      members.push(individual.recordId);
    }
  }

  const classSizeBySignature = new Map<string, number>();
  for (const [signature, members] of groupMembers) {
    classSizeBySignature.set(signature, members.length);
  }

  const cumulativeClasses: CumulativeEquivalenceClass[] = [...groupMembers.entries()]
    .map(([signature, recordIds]) => {
      const first = individuals.get(recordIds[0] ?? "");
      const releaseValues: Readonly<Record<string, Readonly<Record<string, unknown>>>> =
        first?.releaseValues ?? {};
      const size = recordIds.length;
      return {
        signature,
        size,
        recordIds,
        releaseValues,
        unique: size === 1,
        belowThreshold: size < options.threshold,
        reidentificationProbability: size === 0 ? 0 : 1 / size,
      };
    })
    .sort((left, right) => left.size - right.size || left.signature.localeCompare(right.signature));

  const conflicts: CumulativeConflict[] = [];
  const cumulativeIndividuals: CumulativeIndividual[] = [];
  for (const individual of individuals.values()) {
    const conflictResult = detectConflicts(individual, conflictPolicy);
    conflicts.push(...conflictResult.conflicts);
    const signature = individualSignatures.get(individual.recordId) ?? "";
    const classSize = classSizeBySignature.get(signature) ?? 1;
    const baselineSizes = individual.presentReleaseIds.map(
      (releaseId) => releaseClassSizes.get(releaseId)?.get(individual.recordId) ?? 0,
    );
    const baselineMinimumClassSize =
      baselineSizes.length === 0 ? 0 : Math.min(...baselineSizes);
    cumulativeIndividuals.push({
      recordId: individual.recordId,
      presentReleaseIds: [...individual.presentReleaseIds],
      releaseValues: individual.releaseValues,
      conflictingColumns: conflictResult.columns,
      signature,
      classSize,
      baselineMinimumClassSize,
      uniqueBefore: baselineMinimumClassSize === 1,
      uniqueAfter: classSize === 1,
      amplifiedByIntersection: classSize < baselineMinimumClassSize,
    });
  }

  const summary = summarizeClassSizes(
    cumulativeClasses.map((equivalenceClass) => equivalenceClass.size),
    options.threshold,
    cumulativeIndividuals.length,
  );
  const newlyUnique = cumulativeIndividuals.filter(
    (individual) => individual.uniqueAfter && !individual.uniqueBefore,
  );
  const amplified = cumulativeIndividuals.filter(
    (individual) => individual.amplifiedByIntersection,
  );
  const totalReleasedRecordCount = releases.reduce(
    (sum, release) => sum + release.records.length,
    0,
  );
  const cumulativeDistinctRecordCount = cumulativeIndividuals.length;
  const overlapRecordCount = totalReleasedRecordCount - cumulativeDistinctRecordCount;
  const qis = [...new Set(releaseIds.flatMap((releaseId) => releaseQis.get(releaseId) ?? []))];

  const releaseDeltas: ReleaseDelta[] = [];
  const priorUnion = new Set<string>();
  for (const release of releases) {
    const releaseRecordIds = new Set(
      release.records.map((record, index) => {
        const recordIdField = release.recordIdField ?? options.recordIdField ?? "id";
        const raw = record[recordIdField];
        return isMissing(raw) ? `#${index}` : String(raw);
      }),
    );
    const addedRecordIds = [...releaseRecordIds].filter((recordId) => !priorUnion.has(recordId));
    const retainedRecordIds = [...releaseRecordIds].filter((recordId) => priorUnion.has(recordId));
    const droppedRecordIds = [...priorUnion].filter((recordId) => !releaseRecordIds.has(recordId));
    releaseDeltas.push({
      releaseId: release.id,
      addedRecordCount: addedRecordIds.length,
      retainedRecordCount: retainedRecordIds.length,
      droppedRecordCount: droppedRecordIds.length,
      addedRecordIds,
      retainedRecordIds,
      droppedRecordIds,
    });
    for (const recordId of releaseRecordIds) {
      priorUnion.add(recordId);
    }
  }

  const recommendation = recommendCumulativeGeneralization(
    cumulativeIndividuals,
    releaseIds,
    releaseQis,
    {
      threshold: options.threshold,
      hierarchies: options.generalizationHierarchies ?? [],
    },
  );

  return {
    ...summary,
    threshold: options.threshold,
    releaseCount: releases.length,
    releases: releaseAssessments,
    releaseDeltas,
    qis,
    classes: cumulativeClasses,
    flaggedClasses: cumulativeClasses.filter((equivalenceClass) => equivalenceClass.belowThreshold),
    individuals: cumulativeIndividuals,
    conflicts,
    recommendation,
    totalReleasedRecordCount,
    cumulativeDistinctRecordCount,
    overlapRecordCount,
    averageReleasesPerIndividual:
      cumulativeDistinctRecordCount === 0
        ? 0
        : totalReleasedRecordCount / cumulativeDistinctRecordCount,
    uniqueBeforeCount: cumulativeIndividuals.filter((individual) => individual.uniqueBefore).length,
    uniqueAfterCount: cumulativeIndividuals.filter((individual) => individual.uniqueAfter).length,
    newlyUniqueRecordCount: newlyUnique.length,
    amplifiedRecordCount: amplified.length,
    newlyUniqueRecordIds: newlyUnique.map((individual) => individual.recordId),
    amplifiedRecordIds: amplified.map((individual) => individual.recordId),
  };
}
