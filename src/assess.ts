import { assertThreshold } from "./canonical.ts";
import { assessQuasiIdentifierCombinations } from "./equivalence.ts";
import { assessCrossDatasetJoin } from "./join.ts";
import type {
  ReidentificationAssessment,
  ReidentificationAssessmentInput,
} from "./types.ts";

export const DEFAULT_REIDENTIFICATION_THRESHOLD = 5;

/**
 * Single-release re-identification assessment.
 *
 * Every supplied quasi-identifier combination is evaluated independently.
 * When an auxiliary dataset is supplied, each combination is also joined
 * against it to surface identity-level uniquification.
 */
export function assessReidentification(
  input: ReidentificationAssessmentInput,
): ReidentificationAssessment {
  const threshold = input.threshold ?? DEFAULT_REIDENTIFICATION_THRESHOLD;
  assertThreshold(threshold);

  const report = assessQuasiIdentifierCombinations(
    input.dataset,
    input.quasiIdentifierCombinations,
    {
      threshold,
      ...(input.missingValuePolicy === undefined
        ? {}
        : { missingValuePolicy: input.missingValuePolicy }),
      ...(input.recordIdField === undefined
        ? {}
        : { recordIdField: input.recordIdField }),
    },
  );

  const auxiliary = input.auxiliary;
  const joins =
    auxiliary === undefined
      ? []
      : report.combinations.map((assessment) =>
          assessCrossDatasetJoin(input.dataset, auxiliary.dataset, {
            qis: assessment.qis,
            ...(auxiliary.qis === undefined ? {} : { auxiliaryQis: auxiliary.qis }),
            ...(input.recordIdField === undefined
              ? {}
              : { targetRecordIdField: input.recordIdField }),
            ...(auxiliary.recordIdField === undefined
              ? {}
              : { auxiliaryRecordIdField: auxiliary.recordIdField }),
            threshold,
          }),
        );

  return {
    threshold,
    combinations: report.combinations,
    flaggedCombinations: report.flaggedCombinations,
    overallFlagged:
      report.overallFlagged || joins.some((join) => join.riskLevel !== "low"),
    joins,
  };
}
