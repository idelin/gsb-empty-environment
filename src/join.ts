import {
  assertNonEmptyUniqueColumns,
  assertThreshold,
  isMissing,
  signatureForValues,
} from "./canonical.ts";
import { assessQuasiIdentifiers, median } from "./equivalence.ts";
import type {
  CrossDatasetJoinAssessment,
  Dataset,
  JoinMatch,
  JoinOptions,
} from "./types.ts";

function identityFor(
  record: Readonly<Record<string, unknown>>,
  index: number,
  recordIdField: string | undefined,
): string {
  if (recordIdField !== undefined) {
    const value = record[recordIdField];
    if (!isMissing(value)) {
      return String(value);
    }
  }
  return `#${index}`;
}

interface AuxiliaryBucket {
  readonly identities: Set<string>;
  readonly recordIndexes: number[];
}

export function assessCrossDatasetJoin(
  target: Dataset,
  auxiliary: Dataset,
  options: JoinOptions,
): CrossDatasetJoinAssessment {
  assertNonEmptyUniqueColumns(options.qis, "qis");
  const auxiliaryQis = options.auxiliaryQis ?? options.qis;
  assertNonEmptyUniqueColumns(auxiliaryQis, "auxiliaryQis");
  if (auxiliaryQis.length !== options.qis.length) {
    throw new Error("auxiliaryQis must be aligned with qis and have the same length");
  }
  const threshold = options.threshold ?? 5;
  assertThreshold(threshold);

  const targetRecordIdField = options.targetRecordIdField ?? target.recordIdField ?? "id";
  const auxiliaryRecordIdField =
    options.auxiliaryRecordIdField ?? auxiliary.recordIdField ?? "id";

  const targetAssessment = assessQuasiIdentifiers(target, options.qis, {
    threshold,
    recordIdField: targetRecordIdField,
  });

  const targetClassSizeByIndex = new Map<number, number>();
  for (const equivalenceClass of targetAssessment.classes) {
    for (const index of equivalenceClass.recordIndexes) {
      targetClassSizeByIndex.set(index, equivalenceClass.size);
    }
  }

  const auxiliaryBuckets = new Map<string, AuxiliaryBucket>();
  const auxiliaryIdentities = new Set<string>();
  auxiliary.records.forEach((record, index) => {
    const values = auxiliaryQis.map((column) => record[column]);
    const signature = signatureForValues(values);
    const identity = identityFor(record, index, auxiliaryRecordIdField);
    auxiliaryIdentities.add(identity);
    const existing = auxiliaryBuckets.get(signature);
    if (existing === undefined) {
      auxiliaryBuckets.set(signature, {
        identities: new Set<string>([identity]),
        recordIndexes: [index],
      });
      return;
    }
    existing.identities.add(identity);
    existing.recordIndexes.push(index);
  });

  const matches: JoinMatch[] = target.records.map((record, index) => {
    const values = options.qis.map((column) => record[column]);
    const signature = signatureForValues(values);
    const bucket = auxiliaryBuckets.get(signature);
    const targetClassSize = targetClassSizeByIndex.get(index) ?? 1;
    const identities = bucket === undefined ? [] : [...bucket.identities].sort();
    const auxiliaryCandidateCount = identities.length;
    const uniqueAuxiliaryCandidate = auxiliaryCandidateCount === 1;
    const uniquelyReidentified = targetClassSize === 1 && uniqueAuxiliaryCandidate;

    return {
      targetRecordIndex: index,
      targetRecordId: identityFor(record, index, targetRecordIdField),
      signature,
      targetClassSize,
      auxiliaryCandidateCount,
      auxiliaryIdentities: identities,
      matched: auxiliaryCandidateCount > 0,
      uniqueAuxiliaryCandidate,
      uniquelyReidentified,
      narrowedToSingleIdentity: targetClassSize > 1 && uniqueAuxiliaryCandidate,
      candidatePairs: targetClassSize * auxiliaryCandidateCount,
    };
  });

  const matchedMatches = matches.filter((match) => match.matched);
  const candidateCounts = matchedMatches.map((match) => match.auxiliaryCandidateCount);
  const uniquelyReidentifiedRecordCount = matches.filter(
    (match) => match.uniquelyReidentified,
  ).length;
  const uniqueAuxiliaryCandidateCount = matches.filter(
    (match) => match.uniqueAuxiliaryCandidate,
  ).length;
  const narrowedToSingleIdentityCount = matches.filter(
    (match) => match.narrowedToSingleIdentity,
  ).length;
  const baselineUniqueTargetRecordCount = matches.filter(
    (match) => match.targetClassSize === 1,
  ).length;

  const riskLevel: CrossDatasetJoinAssessment["riskLevel"] =
    uniquelyReidentifiedRecordCount > 0
      ? "high"
      : narrowedToSingleIdentityCount > 0 || uniqueAuxiliaryCandidateCount > 0
        ? "medium"
        : "low";

  return {
    qis: [...options.qis],
    auxiliaryQis: [...auxiliaryQis],
    targetDatasetName: target.name ?? "target",
    auxiliaryDatasetName: auxiliary.name ?? "auxiliary",
    targetRecordCount: target.records.length,
    auxiliaryRecordCount: auxiliary.records.length,
    auxiliaryIdentityCount: auxiliaryIdentities.size,
    matchedTargetRecordCount: matchedMatches.length,
    unmatchedTargetRecordCount: matches.length - matchedMatches.length,
    ambiguousTargetRecordCount: matches.filter(
      (match) => match.auxiliaryCandidateCount > 1,
    ).length,
    uniqueAuxiliaryCandidateCount,
    uniqueAuxiliaryCandidateRatio:
      matches.length === 0 ? 0 : uniqueAuxiliaryCandidateCount / matches.length,
    uniquelyReidentifiedRecordCount,
    uniquelyReidentifiedRatio:
      matches.length === 0 ? 0 : uniquelyReidentifiedRecordCount / matches.length,
    narrowedToSingleIdentityCount,
    baselineUniqueTargetRecordCount,
    maxCandidateCount: candidateCounts.length === 0 ? 0 : Math.max(...candidateCounts),
    minCandidateCount: candidateCounts.length === 0 ? 0 : Math.min(...candidateCounts),
    medianCandidateCount: median(candidateCounts),
    riskLevel,
    targetClasses: targetAssessment,
    matches,
  };
}
