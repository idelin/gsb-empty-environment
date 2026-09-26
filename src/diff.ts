import { validate, type SchemaNode, type ValidationError, type ValidationResult } from "./validator.ts";
import { digest, shortDigest } from "./digest.ts";
import { ResultCache } from "./cache.ts";

export interface Sample {
  id: string;
  data: unknown;
}

/** Stable identity of a failing rule: which keyword failed where. */
function ruleSignature(error: ValidationError): string {
  return `${error.keyword}@${error.instancePath}`;
}

function signatureSet(errors: ValidationError[]): Set<string> {
  return new Set(errors.map(ruleSignature));
}

export interface FailureEntry {
  sampleId: string;
  errors: ValidationError[];
}

export interface ReasonChangeEntry {
  sampleId: string;
  /** Rule signatures that failed only under the old schema. */
  removed: string[];
  /** Rule signatures that fail only under the new schema. */
  added: string[];
  oldErrors: ValidationError[];
  newErrors: ValidationError[];
}

export interface DiffReport {
  schemaDigests: { old: string; new: string };
  samplesDigest: string;
  warnings: { old: string[]; new: string[] };
  /** Passed old schema, fails new schema. Listed separately. */
  newFailures: FailureEntry[];
  /** Failed old schema, passes new schema. */
  fixed: string[];
  /** Fails both, but the set of failing rules changed. */
  reasonChanged: ReasonChangeEntry[];
  /** Fails both with the identical set of failing rules. */
  unchangedFailures: FailureEntry[];
  /** Passes both schemas. */
  stillPassing: string[];
}

export interface SampleVerdict {
  id: string;
  valid: boolean;
  errors: ValidationError[];
}

interface RunPayload {
  verdicts: SampleVerdict[];
  warnings: string[];
}

/**
 * Validate every sample against one schema, using the digest-bound cache when
 * provided. Cache hits are only possible when both schema and sample digests
 * match exactly, so edited samples always trigger revalidation.
 */
export function runValidation(
  schema: SchemaNode,
  samples: Sample[],
  cache: ResultCache<RunPayload> | null,
): { payload: RunPayload; cacheHit: boolean } {
  if (cache) {
    const hit = cache.get(schema, samples);
    if (hit) return { payload: hit, cacheHit: true };
  }
  const warnings: string[] = [];
  const verdicts = samples.map((sample) => {
    const result: ValidationResult = validate(sample.data, schema);
    warnings.push(...result.warnings);
    return { id: sample.id, valid: result.valid, errors: result.errors };
  });
  const payload: RunPayload = { verdicts, warnings: [...new Set(warnings)] };
  if (cache) cache.set(schema, samples, payload);
  return { payload, cacheHit: false };
}

export function diffReports(oldRun: RunPayload, newRun: RunPayload, oldSchema: unknown, newSchema: unknown, samples: Sample[]): DiffReport {
  const report: DiffReport = {
    schemaDigests: { old: shortDigest(oldSchema), new: shortDigest(newSchema) },
    samplesDigest: shortDigest(samples),
    warnings: { old: oldRun.warnings, new: newRun.warnings },
    newFailures: [],
    fixed: [],
    reasonChanged: [],
    unchangedFailures: [],
    stillPassing: [],
  };

  const newById = new Map(newRun.verdicts.map((v) => [v.id, v]));
  for (const oldVerdict of oldRun.verdicts) {
    const newVerdict = newById.get(oldVerdict.id);
    if (!newVerdict) continue;

    if (oldVerdict.valid && newVerdict.valid) {
      report.stillPassing.push(oldVerdict.id);
    } else if (oldVerdict.valid && !newVerdict.valid) {
      report.newFailures.push({ sampleId: oldVerdict.id, errors: newVerdict.errors });
    } else if (!oldVerdict.valid && newVerdict.valid) {
      report.fixed.push(oldVerdict.id);
    } else {
      const oldSigs = signatureSet(oldVerdict.errors);
      const newSigs = signatureSet(newVerdict.errors);
      const removed = [...oldSigs].filter((s) => !newSigs.has(s)).sort();
      const added = [...newSigs].filter((s) => !oldSigs.has(s)).sort();
      if (removed.length === 0 && added.length === 0) {
        report.unchangedFailures.push({ sampleId: oldVerdict.id, errors: newVerdict.errors });
      } else {
        report.reasonChanged.push({
          sampleId: oldVerdict.id,
          removed,
          added,
          oldErrors: oldVerdict.errors,
          newErrors: newVerdict.errors,
        });
      }
    }
  }
  return report;
}

export interface CompareOptions {
  cacheDir?: string;
}

export interface CompareResult {
  report: DiffReport;
  cache: { old: "hit" | "miss" | "off"; new: "hit" | "miss" | "off" };
}

export function compareSchemas(oldSchema: SchemaNode, newSchema: SchemaNode, samples: Sample[], options: CompareOptions = {}): CompareResult {
  const cache = options.cacheDir ? new ResultCache<RunPayload>(options.cacheDir) : null;
  const oldRun = runValidation(oldSchema, samples, cache);
  const newRun = runValidation(newSchema, samples, cache);
  return {
    report: diffReports(oldRun.payload, newRun.payload, oldSchema, newSchema, samples),
    cache: {
      old: cache ? (oldRun.cacheHit ? "hit" : "miss") : "off",
      new: cache ? (newRun.cacheHit ? "hit" : "miss") : "off",
    },
  };
}

export function formatError(e: ValidationError): string {
  return `instance ${e.instancePath || "/"} | rule ${e.rulePath || "/"} (${e.keyword}): ${e.message}`;
}

export function renderReport(result: CompareResult): string {
  const { report, cache } = result;
  const lines: string[] = [];
  lines.push(`schema digests: old=${report.schemaDigests.old} new=${report.schemaDigests.new}`);
  lines.push(`samples digest: ${report.samplesDigest}`);
  lines.push(`cache: old=${cache.old} new=${cache.new}`);
  if (report.warnings.old.length || report.warnings.new.length) {
    lines.push(`warnings (unsupported keywords ignored):`);
    for (const w of [...new Set([...report.warnings.old, ...report.warnings.new])]) lines.push(`  ! ${w}`);
  }

  lines.push(`\nNEW FAILURES (old pass -> new fail): ${report.newFailures.length}`);
  for (const f of report.newFailures) {
    lines.push(`  [${f.sampleId}]`);
    for (const e of f.errors) lines.push(`    ${formatError(e)}`);
  }

  lines.push(`\nFIXED (old fail -> new pass): ${report.fixed.length}`);
  for (const id of report.fixed) lines.push(`  [${id}]`);

  lines.push(`\nREASON CHANGED (fail both, different rules): ${report.reasonChanged.length}`);
  for (const r of report.reasonChanged) {
    lines.push(`  [${r.sampleId}] removed: ${r.removed.join(", ") || "-"} | added: ${r.added.join(", ") || "-"}`);
    for (const e of r.newErrors) lines.push(`    now: ${formatError(e)}`);
  }

  lines.push(`\nUNCHANGED FAILURES (fail both, same rules): ${report.unchangedFailures.length}`);
  for (const f of report.unchangedFailures) lines.push(`  [${f.sampleId}]`);

  lines.push(`\nSTILL PASSING: ${report.stillPassing.length}`);
  for (const id of report.stillPassing) lines.push(`  [${id}]`);
  return lines.join("\n");
}
