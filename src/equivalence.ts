import {
  assertNonEmptyUniqueColumns,
  assertThreshold,
  isMissing,
  signatureForColumns,
  valuesForColumns,
} from "./canonical.ts";
import type {
  AssessmentOptions,
  Dataset,
  EquivalenceClass,
  QIAssessment,
  QuasiIdentifierReport,
  RiskSummary,
} from "./types.ts";

export function arithmeticMean(values: readonly number[]): number {
  if (values.length === 0) {
    return 0;
  }
  return values.reduce((sum, value) => sum + value, 0) / values.length;
}

export function median(values: readonly number[]): number {
  if (values.length === 0) {
    return 0;
  }
  const sorted = [...values].sort((left, right) => left - right);
  const middle = Math.floor(sorted.length / 2);
  if (sorted.length % 2 === 1) {
    return sorted[middle] ?? 0;
  }
  return ((sorted[middle - 1] ?? 0) + (sorted[middle] ?? 0)) / 2;
}

export function summarizeClassSizes(
  classSizes: readonly number[],
  threshold: number,
  recordCount: number,
): RiskSummary {
  const sorted = [...classSizes].sort((left, right) => left - right);
  const equivalenceClassCount = sorted.length;
  const minClassSize = equivalenceClassCount === 0 ? 0 : (sorted[0] ?? 0);
  const maxClassSize = equivalenceClassCount === 0 ? 0 : (sorted[sorted.length - 1] ?? 0);
  const uniqueClasses = sorted.filter((size) => size === 1);
  const belowThreshold = sorted.filter((size) => size < threshold);
  const belowThresholdRecordCount = belowThreshold.reduce((sum, size) => sum + size, 0);

  return {
    recordCount,
    equivalenceClassCount,
    classSizes: sorted,
    minClassSize,
    medianClassSize: median(sorted),
    meanClassSize: arithmeticMean(sorted),
    maxClassSize,
    uniqueClassCount: uniqueClasses.length,
    uniqueRecordCount: uniqueClasses.length,
    belowThresholdClassCount: belowThreshold.length,
    belowThresholdRecordCount,
    atRiskRecordRatio: recordCount === 0 ? 0 : belowThresholdRecordCount / recordCount,
    maxReidentificationProbability: minClassSize === 0 ? 0 : 1 / minClassSize,
    flagged: belowThreshold.length > 0,
  };
}

function recordIdentity(record: Dataset["records"][number], index: number, recordIdField?: string): string {
  if (recordIdField !== undefined) {
    const value = record[recordIdField];
    if (!isMissing(value)) {
      return String(value);
    }
  }
  return `#${index}`;
}

export function assessQuasiIdentifiers(
  dataset: Dataset,
  qis: readonly string[],
  options: AssessmentOptions,
): QIAssessment {
  assertNonEmptyUniqueColumns(qis, "qis");
  assertThreshold(options.threshold);

  const missingValuePolicy = options.missingValuePolicy ?? "as-value";
  const recordIdField = options.recordIdField ?? dataset.recordIdField ?? "id";
  const groups = new Map<string, {
    values: Readonly<Record<string, unknown>>;
    recordIndexes: number[];
    recordIds: string[];
  }>();
  let excludedRecordCount = 0;
  let includedRecordCount = 0;

  dataset.records.forEach((record, index) => {
    const values = valuesForColumns(record, qis);
    if (missingValuePolicy === "exclude" && qis.some((column) => isMissing(values[column]))) {
      excludedRecordCount += 1;
      return;
    }

    includedRecordCount += 1;
    const signature = signatureForColumns(qis, values);
    const existing = groups.get(signature);
    if (existing === undefined) {
      groups.set(signature, {
        values,
        recordIndexes: [index],
        recordIds: [recordIdentity(record, index, recordIdField)],
      });
      return;
    }
    existing.recordIndexes.push(index);
    existing.recordIds.push(recordIdentity(record, index, recordIdField));
  });

  const classes: EquivalenceClass[] = [...groups.entries()]
    .map(([signature, group]) => {
      const size = group.recordIndexes.length;
      return {
        signature,
        values: group.values,
        size,
        recordIndexes: group.recordIndexes,
        recordIds: group.recordIds,
        unique: size === 1,
        belowThreshold: size < options.threshold,
        reidentificationProbability: size === 0 ? 0 : 1 / size,
      };
    })
    .sort((left, right) => left.size - right.size || left.signature.localeCompare(right.signature));

  const summary = summarizeClassSizes(
    classes.map((equivalenceClass) => equivalenceClass.size),
    options.threshold,
    includedRecordCount,
  );

  return {
    ...summary,
    qis: [...qis],
    threshold: options.threshold,
    inputRecordCount: dataset.records.length,
    excludedRecordCount,
    classes,
    flaggedClasses: classes.filter((equivalenceClass) => equivalenceClass.belowThreshold),
  };
}

export function assessQuasiIdentifierCombinations(
  dataset: Dataset,
  combinations: readonly (readonly string[])[],
  options: AssessmentOptions,
): QuasiIdentifierReport {
  if (combinations.length === 0) {
    throw new Error("at least one quasi-identifier combination is required");
  }

  const assessments = combinations.map((combination) =>
    assessQuasiIdentifiers(dataset, combination, options),
  );

  return {
    threshold: options.threshold,
    combinations: assessments,
    flaggedCombinations: assessments.filter((assessment) => assessment.flagged),
    overallFlagged: assessments.some((assessment) => assessment.flagged),
  };
}
