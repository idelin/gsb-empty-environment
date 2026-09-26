import type { Json, Schema, SchemaObject, ValidationError, Verification } from './types.ts';
import { canonical, keywordChecks } from './keywords.ts';
import { getPointer, parsePointer } from './pointer.ts';
import { validate } from './validator.ts';

/**
 * Independently verify that a diagnosis is sufficient:
 *  - every reported atomic error is re-checked against the finite
 *    sub-instance at its instancePath and the rule at its schemaPath;
 *  - for a failed combinator, every failed branch is re-validated and must
 *    be hit by at least one selected error (unless the diagnosis itself
 *    declares incompleteness consistently);
 *  - for a oneOf ambiguity, the witness instance must really validate
 *    against every branch it names.
 */
export function verifyDiagnosis(schema: Schema, instance: Json, errors: ValidationError[]): Verification {
  const problems: string[] = [];
  for (const error of errors) verifyError(schema, instance, error, problems);
  return { sufficient: problems.length === 0, problems };
}

/**
 * Rule identity relative to a combinator location: branch errors recomputed
 * during verification are rooted at the combinator's instance, while the
 * reported selected errors carry absolute instance paths. Comparing keyword
 * + relative instance path + rule parameters makes the two comparable.
 */
function relativeRuleId(
  error: ValidationError,
  baseSegments: string[],
  label: string,
  problems: string[],
): string {
  const segments = parsePointer(error.instancePath);
  const isInside = baseSegments.every((s, i) => segments[i] === s);
  if (!isInside) {
    problems.push(`${label}: selected error ${error.instancePath} lies outside the combinator instance`);
  }
  const relative = segments.slice(baseSegments.length);
  return `${error.keyword}@/${relative.join('/')}:${canonical(error.params ?? {})}`;
}

function verifyError(schema: Schema, instance: Json, error: ValidationError, problems: string[]): void {
  const label = `${error.keyword} at ${error.instancePath} (${error.schemaPath})`;
  if (error.diagnosis) {
    verifyCombinator(schema, instance, error, problems);
    return;
  }
  const segments = parsePointer(error.schemaPath);
  const keyword = segments[segments.length - 1];
  const subInstance = getPointer(instance, parsePointer(error.instancePath));
  if (keyword === 'false') {
    if (getPointer(schema, segments) !== false) {
      problems.push(`${label}: schema at schemaPath is not boolean false`);
    }
    return;
  }
  if (keyword !== error.keyword) {
    problems.push(`${label}: schemaPath does not end at the reported keyword`);
    return;
  }
  const parent = getPointer(schema, segments.slice(0, -1));
  if (parent === null || typeof parent !== 'object' || Array.isArray(parent)) {
    problems.push(`${label}: cannot locate parent schema`);
    return;
  }
  const check = keywordChecks[error.keyword];
  if (!check) {
    problems.push(`${label}: keyword not in the implemented subset, cannot verify`);
    return;
  }
  const message = check((parent as SchemaObject)[error.keyword], subInstance as Json);
  if (message === null) {
    problems.push(`${label}: rule re-check passes, error is not reproducible`);
  }
}

function verifyCombinator(
  schema: Schema,
  instance: Json,
  error: ValidationError,
  problems: string[],
): void {
  const diagnosis = error.diagnosis!;
  const label = `${error.keyword} at ${error.instancePath} (${error.schemaPath})`;
  const segments = parsePointer(error.schemaPath);
  if (segments[segments.length - 1] !== diagnosis.kind) {
    problems.push(`${label}: schemaPath does not end at the combinator keyword`);
    return;
  }
  const parent = getPointer(schema, segments.slice(0, -1)) as SchemaObject | undefined;
  const branches = parent?.[diagnosis.kind];
  if (!Array.isArray(branches)) {
    problems.push(`${label}: cannot locate branch schemas`);
    return;
  }
  const baseSegments = parsePointer(error.instancePath);
  const subInstance = getPointer(instance, baseSegments) as Json;

  // Independently re-validate every branch against the finite sub-instance.
  const recomputed = (branches as Schema[]).map((sub) => validate(sub, subInstance));
  const recomputedMatched = recomputed.map((e, i) => (e.length === 0 ? i : -1)).filter((i) => i >= 0);

  if (JSON.stringify(recomputedMatched) !== JSON.stringify(diagnosis.matched)) {
    problems.push(
      `${label}: matched branches ${JSON.stringify(diagnosis.matched)} disagree with ` +
        `independent re-validation ${JSON.stringify(recomputedMatched)}`,
    );
    return;
  }

  if (diagnosis.reason === 'multiple-branches-matched') {
    const witness = diagnosis.witness;
    if (!witness || witness.matchedBranches.length < 2) {
      problems.push(`${label}: ambiguity reported without a witness naming >= 2 branches`);
      return;
    }
    for (const branchIndex of witness.matchedBranches) {
      if (recomputed[branchIndex] === undefined || recomputed[branchIndex].length !== 0) {
        problems.push(`${label}: witness branch ${branchIndex} does not actually validate`);
      }
    }
    return;
  }

  // reason === 'no-branch-matched': the selected set must refute every
  // failed branch (coverage), and every selected error must be real.
  const selectedIds = new Set(
    diagnosis.selectedErrors.map((e) => relativeRuleId(e, baseSegments, label, problems)),
  );
  const expectedUncovered: number[] = [];
  recomputed.forEach((branchErrors, branchIndex) => {
    const hit = branchErrors.some((e) => selectedIds.has(relativeRuleId(e, [], label, problems)));
    if (!hit) expectedUncovered.push(branchIndex);
  });
  if (diagnosis.complete) {
    for (const branchIndex of expectedUncovered) {
      problems.push(`${label}: branch ${branchIndex} fails but no selected error refutes it`);
    }
  } else {
    const declared = [...(diagnosis.uncoveredBranches ?? [])].sort((a, b) => a - b);
    const actual = [...expectedUncovered].sort((a, b) => a - b);
    if (JSON.stringify(declared) !== JSON.stringify(actual)) {
      problems.push(
        `${label}: declared uncovered branches ${JSON.stringify(declared)} disagree with ` +
          `independent re-validation ${JSON.stringify(actual)}`,
      );
    }
  }
  for (const selected of diagnosis.selectedErrors) {
    verifyError(schema, instance, selected, problems);
  }
}
