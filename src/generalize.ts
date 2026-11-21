import { canonicalizeQiValue, signatureForValues } from "./canonical.ts";
import { summarizeClassSizes } from "./equivalence.ts";
import type {
  CumulativeIndividual,
  Dataset,
  GeneralizationAction,
  GeneralizationHierarchy,
  GeneralizationProjection,
  PublicationRecommendation,
  RiskSummary,
} from "./types.ts";

interface ValueCell {
  readonly scope: string;
  readonly column: string;
  readonly value: unknown;
}

interface EvaluatedPlan {
  readonly levels: ReadonlyMap<string, number>;
  readonly summary: RiskSummary;
}

interface SearchOptions {
  readonly threshold: number;
  readonly hierarchies: readonly GeneralizationHierarchy[];
}

function assertHierarchies(hierarchies: readonly GeneralizationHierarchy[]): void {
  const seen = new Set<string>();
  for (const hierarchy of hierarchies) {
    if (seen.has(hierarchy.column)) {
      throw new Error(`duplicate generalization hierarchy for column "${hierarchy.column}"`);
    }
    seen.add(hierarchy.column);
    if (hierarchy.levels.length === 0) {
      throw new Error(`generalization hierarchy for "${hierarchy.column}" must have at least one level`);
    }
  }
}

function hierarchyFor(
  hierarchies: readonly GeneralizationHierarchy[],
  column: string,
): GeneralizationHierarchy | undefined {
  return hierarchies.find((hierarchy) => hierarchy.column === column);
}

function maximumLevel(hierarchy: GeneralizationHierarchy | undefined): number {
  const definedLevels = hierarchy?.levels.length ?? 0;
  return Math.max(1, definedLevels);
}

function transformValue(
  value: unknown,
  level: number,
  hierarchy: GeneralizationHierarchy | undefined,
): unknown {
  if (level === 0) {
    return value;
  }
  if (hierarchy !== undefined && level < hierarchy.levels.length) {
    const selected = hierarchy.levels[level];
    if (selected !== undefined) {
      return selected.transform(value);
    }
  }
  return "*";
}

function actionFor(
  column: string,
  level: number,
  hierarchy: GeneralizationHierarchy | undefined,
  releaseIds: readonly string[],
): GeneralizationAction {
  const selected = hierarchy?.levels[level];
  if (selected !== undefined && level < (hierarchy?.levels.length ?? 0)) {
    return {
      column,
      level,
      label: selected.label,
      kind: "generalize",
      releaseIds: [...releaseIds],
    };
  }
  return {
    column,
    level,
    label: "suppress (replace with '*')",
    kind: "suppress",
    releaseIds: [...releaseIds],
  };
}

function summarizeRows(
  rows: readonly (readonly ValueCell[])[],
  levels: ReadonlyMap<string, number>,
  hierarchies: readonly GeneralizationHierarchy[],
  threshold: number,
): RiskSummary {
  const groups = new Map<string, number>();

  for (const row of rows) {
    const keyValues: unknown[] = [];
    for (const cell of row) {
      const hierarchy = hierarchyFor(hierarchies, cell.column);
      const level = levels.get(cell.column) ?? 0;
      keyValues.push(cell.scope, canonicalizeQiValue(transformValue(cell.value, level, hierarchy)));
    }
    const signature = signatureForValues(keyValues);
    groups.set(signature, (groups.get(signature) ?? 0) + 1);
  }

  return summarizeClassSizes([...groups.values()], threshold, rows.length);
}

function scoreImprovement(before: RiskSummary, after: RiskSummary): number {
  const atRiskDrop = before.belowThresholdRecordCount - after.belowThresholdRecordCount;
  const uniqueDrop = before.uniqueClassCount - after.uniqueClassCount;
  const minGain = after.minClassSize - before.minClassSize;
  const classDrop = before.equivalenceClassCount - after.equivalenceClassCount;
  return atRiskDrop * 100_000 + uniqueDrop * 10_000 + minGain * 100 + classDrop;
}

function searchGeneralization(
  rows: readonly (readonly ValueCell[])[],
  columns: readonly string[],
  options: SearchOptions,
): EvaluatedPlan {
  assertHierarchies(options.hierarchies);

  const levels = new Map<string, number>(
    columns.map((column) => [column, 0] as const),
  );
  let current = summarizeRows(rows, levels, options.hierarchies, options.threshold);
  const maximumIterations = Math.max(
    1,
    columns.reduce(
      (sum, column) => sum + maximumLevel(hierarchyFor(options.hierarchies, column)),
      0,
    ),
  );

  for (let iteration = 0; iteration < maximumIterations && current.flagged; iteration += 1) {
    let best:
      | {
          readonly column: string;
          readonly level: number;
          readonly summary: RiskSummary;
          readonly score: number;
        }
      | undefined;

    for (const column of columns) {
      const hierarchy = hierarchyFor(options.hierarchies, column);
      const currentLevel = levels.get(column) ?? 0;
      const nextLevel = currentLevel + 1;
      if (nextLevel > maximumLevel(hierarchy)) {
        continue;
      }

      const candidateLevels = new Map(levels);
      candidateLevels.set(column, nextLevel);
      const summary = summarizeRows(rows, candidateLevels, options.hierarchies, options.threshold);
      const score = scoreImprovement(current, summary);
      if (score <= 0) {
        continue;
      }

      if (
        best === undefined ||
        score > best.score ||
        (score === best.score && nextLevel < best.level)
      ) {
        best = { column, level: nextLevel, summary, score };
      }
    }

    if (best === undefined) {
      break;
    }
    levels.set(best.column, best.level);
    current = best.summary;
  }

  return { levels, summary: current };
}

export function planSingleReleaseGeneralization(
  dataset: Dataset,
  qis: readonly string[],
  options: SearchOptions,
): GeneralizationProjection {
  const rows = dataset.records.map((record) =>
    qis.map<ValueCell>((column) => ({ scope: "release", column, value: record[column] })),
  );
  const before = summarizeRows(
    rows,
    new Map<string, number>(qis.map((column) => [column, 0] as const)),
    options.hierarchies,
    options.threshold,
  );
  const plan = searchGeneralization(rows, qis, options);
  const actions = qis
    .filter((column) => (plan.levels.get(column) ?? 0) > 0)
    .map((column) =>
      actionFor(
        column,
        plan.levels.get(column) ?? 0,
        hierarchyFor(options.hierarchies, column),
        [],
      ),
    );

  return {
    threshold: options.threshold,
    achievable: !plan.summary.flagged,
    actions,
    before,
    projected: plan.summary,
    explanation: plan.summary.flagged
      ? "The configured hierarchies cannot raise every class to the threshold."
      : before.flagged
        ? "Apply the listed generalization actions to eliminate classes below the threshold."
        : "No additional generalization is required.",
  };
}

export function recommendCumulativeGeneralization(
  individuals: readonly CumulativeIndividual[],
  releaseIds: readonly string[],
  releaseQis: ReadonlyMap<string, readonly string[]>,
  options: SearchOptions,
): PublicationRecommendation {
  const columns = [...new Set(releaseIds.flatMap((releaseId) => releaseQis.get(releaseId) ?? []))];
  const releaseOrder = new Map<string, number>(
    releaseIds.map((releaseId, index) => [releaseId, index] as const),
  );
  const rows = individuals.map((individual) => {
    const present = new Set(individual.presentReleaseIds);
    const cells: ValueCell[] = [];
    for (const releaseId of releaseIds) {
      const qis = releaseQis.get(releaseId) ?? [];
      if (!present.has(releaseId)) {
        cells.push({ scope: releaseId, column: "__not_released__", value: "__not_released__" });
        continue;
      }
      const values = individual.releaseValues[releaseId] ?? {};
      for (const column of qis) {
        cells.push({ scope: releaseId, column, value: values[column] });
      }
    }
    cells.sort((left, right) => {
      const scopeOrder =
        (releaseOrder.get(left.scope) ?? 0) - (releaseOrder.get(right.scope) ?? 0);
      return scopeOrder || left.column.localeCompare(right.column);
    });
    return cells;
  });

  const before = summarizeRows(
    rows,
    new Map<string, number>(columns.map((column) => [column, 0] as const)),
    options.hierarchies,
    options.threshold,
  );
  const plan = searchGeneralization(rows, columns, options);
  const releaseIdsByColumn = new Map<string, string[]>();
  for (const releaseId of releaseIds) {
    for (const column of releaseQis.get(releaseId) ?? []) {
      const existing = releaseIdsByColumn.get(column) ?? [];
      existing.push(releaseId);
      releaseIdsByColumn.set(column, existing);
    }
  }
  const actions = columns
    .filter((column) => (plan.levels.get(column) ?? 0) > 0)
    .map((column) =>
      actionFor(
        column,
        plan.levels.get(column) ?? 0,
        hierarchyFor(options.hierarchies, column),
        releaseIdsByColumn.get(column) ?? [],
      ),
    );

  const required = before.flagged;
  const achievable = !plan.summary.flagged;
  const newlyUnique = individuals.filter((individual) => individual.uniqueAfter && !individual.uniqueBefore);
  const amplified = individuals.filter((individual) => individual.amplifiedByIntersection);
  const urgency: PublicationRecommendation["urgency"] = !required
    ? "none"
    : achievable
      ? "corrective"
      : "blocking";
  const scope: PublicationRecommendation["scope"] = required
    ? "supplemental-generalized-release"
    : "none";
  const rationale = required
    ? `Cumulative classes leave ${before.belowThresholdRecordCount} individual(s) below k=${options.threshold}; ` +
      `${newlyUnique.length} individual(s) became unique only after intersecting releases and ` +
      `${amplified.length} individual(s) were amplified.`
    : "Cumulative equivalence classes already meet the threshold.";

  const notes: string[] = [];
  if (required) {
    notes.push(
      "Actions target every previously published copy of the affected column because earlier values cannot be retracted.",
    );
    notes.push(
      "Issue a supplemental generalized release to existing recipients; changing only future releases leaves the cumulative intersection exposed.",
    );
  }
  if (!achievable) {
    notes.push(
      "The available hierarchies cannot reach the threshold; suppress the impacted combination or reduce cross-release overlap.",
    );
  }
  if (newlyUnique.length === 0 && amplified.length === 0 && required) {
    notes.push("Risk is already present in a single release rather than created by release intersection alone.");
  }

  return {
    threshold: options.threshold,
    achievable,
    actions,
    before,
    projected: plan.summary,
    explanation: plan.summary.flagged
      ? "No tested generalization plan reaches the threshold."
      : required
        ? "Apply the listed actions consistently across releases to restore k-anonymity for cumulative data."
        : "No additional generalization is required.",
    required,
    urgency,
    scope,
    rationale,
    notes,
  };
}
