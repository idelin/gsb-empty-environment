import type {
  AdmissionInput,
  AdmissionResult,
  BagCounterexample,
  DatabaseSchema,
  ExecutionCredential,
  PartitionCertificate,
  Plan,
  SqlValue,
  TenantReadPolicy,
} from "./types.ts";
import {
  digest,
  normalizePlan,
  PlanSyntaxError,
  planDigest,
  validatePlan,
} from "./ast.ts";
import { analyzePlans } from "./analysis.ts";
import { evaluatePlan } from "./eval.ts";
import {
  DEFAULT_MAX_CLASSES,
  deriveRepresentativeDomain,
  enumerateDomainClasses,
  certificateDigest,
  RepresentativeLimitError,
} from "./representatives.ts";
import { normalizePolicy } from "./policy.ts";
import { valueKey } from "./value.ts";

function bagCounts(rows: readonly (readonly SqlValue[])[]): Map<string, { row: readonly SqlValue[]; count: number }> {
  const result = new Map<string, { row: readonly SqlValue[]; count: number }>();
  for (const row of rows) {
    const key = JSON.stringify(row.map(valueKey));
    const current = result.get(key);
    if (current === undefined) result.set(key, { row, count: 1 });
    else current.count += 1;
  }
  return result;
}

function compareBags(
  referenceRows: readonly (readonly SqlValue[])[],
  candidateRows: readonly (readonly SqlValue[])[],
): {
  equal: boolean;
  missing: { row: readonly SqlValue[]; count: number }[];
  extra: { row: readonly SqlValue[]; count: number }[];
} {
  const reference = bagCounts(referenceRows);
  const candidate = bagCounts(candidateRows);
  const missing: { row: readonly SqlValue[]; count: number }[] = [];
  const extra: { row: readonly SqlValue[]; count: number }[] = [];

  for (const [key, entry] of reference) {
    const other = candidate.get(key);
    if (other === undefined) missing.push({ row: entry.row, count: entry.count });
    else if (other.count < entry.count) {
      missing.push({ row: entry.row, count: entry.count - other.count });
    }
  }
  for (const [key, entry] of candidate) {
    const other = reference.get(key);
    if (other === undefined) extra.push({ row: entry.row, count: entry.count });
    else if (other.count < entry.count) {
      extra.push({ row: entry.row, count: entry.count - other.count });
    }
  }
  missing.sort((left, right) =>
    JSON.stringify(left.row.map(valueKey)).localeCompare(JSON.stringify(right.row.map(valueKey))),
  );
  extra.sort((left, right) =>
    JSON.stringify(left.row.map(valueKey)).localeCompare(JSON.stringify(right.row.map(valueKey))),
  );
  return { equal: missing.length === 0 && extra.length === 0, missing, extra };
}

function policyVersion(policy: TenantReadPolicy | undefined): string {
  return String(
    policy?.version ?? policy?.policyVersion ?? policy?.revision ?? digest(policy ?? {}),
  );
}

function certificateFrom(
  reference: Plan,
  candidate: Plan,
  policy: TenantReadPolicy | undefined,
  schema: DatabaseSchema | undefined,
  equivalent: boolean,
  domain: ReturnType<typeof deriveRepresentativeDomain>,
  classes: ReturnType<typeof enumerateDomainClasses>["classes"],
): PartitionCertificate {
  const root = {
    format: "bag-partition-certificate-v1" as const,
    referencePlanDigest: planDigest(reference),
    candidatePlanDigest: planDigest(candidate),
    policyVersion: policyVersion(policy),
    domain,
    equivalent,
    classes,
    classCount: classes.length,
  };
  void schema;
  return { ...root, digest: certificateDigest(root) };
}

function credentialFor(
  candidate: Plan,
  reference: Plan,
  policy: TenantReadPolicy | undefined,
  certificate: PartitionCertificate,
): ExecutionCredential {
  const planDigestValue = planDigest(candidate);
  const referencePlanDigest = planDigest(reference);
  const version = policyVersion(policy);
  const body = {
    format: "plan-execution-credential-v1" as const,
    planDigest: planDigestValue,
    referencePlanDigest,
    policyVersion: version,
    certificateDigest: certificate.digest,
  };
  return {
    ...body,
    token: digest(body),
  };
}

function normalizeAdmissionInput(
  input: AdmissionInput | Plan,
  candidateArg?: Plan,
  policyArg?: TenantReadPolicy,
  schemaArg?: DatabaseSchema,
): AdmissionInput {
  if ("kind" in input && (
    input.kind === "scan" || input.kind === "join" || input.kind === "filter" ||
    input.kind === "project" || input.kind === "unionAll"
  )) {
    return {
      reference: input,
      candidate: candidateArg,
      policy: policyArg ?? {},
      schema: schemaArg,
    };
  }
  return input;
}

function admitPlanInternal(
  inputOrReference: AdmissionInput | Plan,
  candidateArg?: Plan,
  policyArg?: TenantReadPolicy,
  schemaArg?: DatabaseSchema,
): AdmissionResult {
  const input = normalizeAdmissionInput(inputOrReference, candidateArg, policyArg, schemaArg);
  const loose = input as AdmissionInput & {
    readonly ref?: Plan;
    readonly cand?: Plan;
    readonly tenantPolicy?: TenantReadPolicy;
  };
  const policy = input.policy ?? loose.tenantPolicy ?? {};
  const rawReference = input.reference ?? input.referencePlan ?? loose.ref;
  const rawCandidate = input.candidate ?? input.candidatePlan ?? loose.cand;

  if (rawReference === undefined || rawCandidate === undefined) {
    const emptyDigest = digest({});
    return {
      admitted: false,
      ok: false,
      reason: "Both reference and candidate plans are required",
      planDigest: rawCandidate === undefined ? emptyDigest : planDigest(rawCandidate),
      referencePlanDigest: rawReference === undefined ? emptyDigest : planDigest(rawReference),
      policyVersion: policyVersion(policy),
    };
  }

  let reference: Plan;
  let candidate: Plan;
  try {
    reference = normalizePlan(rawReference);
    candidate = normalizePlan(rawCandidate);
    validatePlan(reference);
    validatePlan(candidate);
  } catch (error) {
    const message = error instanceof PlanSyntaxError || error instanceof Error
      ? error.message
      : String(error);
    return {
      admitted: false,
      ok: false,
      reason: `Plan rejected by the admission grammar: ${message}`,
      planDigest: planDigest(rawCandidate),
      referencePlanDigest: planDigest(rawReference),
      policyVersion: policyVersion(policy),
    };
  }

  const maxClasses = input.maxRepresentativeClasses ?? DEFAULT_MAX_CLASSES;
  const domain = deriveRepresentativeDomain(
    [reference, candidate],
    policy,
    input.schema,
  );
  let enumeration;
  try {
    enumeration = enumerateDomainClasses(domain, maxClasses);
  } catch (error) {
    const reason = error instanceof RepresentativeLimitError
      ? error.message
      : `Representative enumeration failed: ${String(error)}`;
    return {
      admitted: false,
      ok: false,
      reason,
      planDigest: planDigest(candidate),
      referencePlanDigest: planDigest(reference),
      policyVersion: policyVersion(policy),
    };
  }

  if (enumeration.truncated) {
    return {
      admitted: false,
      ok: false,
      reason: `Representative universe exceeds ${maxClasses} classes; refusing admission`,
      planDigest: planDigest(candidate),
      referencePlanDigest: planDigest(reference),
      policyVersion: policyVersion(policy),
    };
  }

  const analysis = analyzePlans(
    [reference, candidate],
    policy,
    input.schema,
  );
  const referencePolicy = normalizePolicy(policy);
  let counterexample: BagCounterexample | undefined;

  for (const entry of enumeration.classes) {
    const referenceRows = evaluatePlan(reference, entry.database, referencePolicy.source, {
      columnsByTable: analysis.columnsByTable,
    });
    const candidateRows = evaluatePlan(
      candidate,
      entry.database,
      input.candidatePolicy ?? undefined,
      { columnsByTable: analysis.columnsByTable },
    );
    const comparison = compareBags(referenceRows, candidateRows);
    if (!comparison.equal) {
      counterexample = {
        classKey: entry.key,
        database: entry.database,
        referenceRows,
        candidateRows,
        missing: comparison.missing,
        extra: comparison.extra,
      };
      break;
    }
  }

  const equivalent = counterexample === undefined;
  const certificate = certificateFrom(
    reference,
    candidate,
    policy,
    input.schema,
    equivalent,
    domain,
    enumeration.classes,
  );

  if (!equivalent) {
    return {
      admitted: false,
      ok: false,
      reason: "Candidate is not bag-equivalent to the policy-filtered reference",
      planDigest: planDigest(candidate),
      referencePlanDigest: planDigest(reference),
      policyVersion: policyVersion(policy),
      certificate,
      counterexample,
    };
  }

  return {
    admitted: true,
    ok: true,
    reason: "Candidate is equivalent within the complete two-table bag boundary",
    planDigest: planDigest(candidate),
    referencePlanDigest: planDigest(reference),
    policyVersion: policyVersion(policy),
    certificate,
    credential: credentialFor(candidate, reference, policy, certificate),
  };
}

function withResultAliases(result: AdmissionResult): AdmissionResult {
  return { ...result, allowed: result.admitted, authorized: result.admitted };
}

export function admitPlan(
  inputOrReference: AdmissionInput | Plan,
  candidateArg?: Plan,
  policyArg?: TenantReadPolicy,
  schemaArg?: DatabaseSchema,
): AdmissionResult {
  return withResultAliases(
    admitPlanInternal(inputOrReference, candidateArg, policyArg, schemaArg),
  );
}

export const admit = admitPlan;
export const admitQueryPlan = admitPlan;
export const authorizePlan = admitPlan;
export const checkPlanEquivalence = admitPlan;
export const checkPlanAdmission = admitPlan;

export function plansEquivalent(
  inputOrReference: AdmissionInput | Plan,
  candidateArg?: Plan,
  policyArg?: TenantReadPolicy,
  schemaArg?: DatabaseSchema,
): boolean {
  return admitPlan(inputOrReference, candidateArg, policyArg, schemaArg).admitted;
}

export function verifyExecutionCredential(
  credential: ExecutionCredential,
  candidate?: Plan | AdmissionResult,
  reference?: Plan,
  policy?: TenantReadPolicy,
  certificate?: PartitionCertificate,
): boolean {
  const body = {
    format: credential.format,
    planDigest: credential.planDigest,
    referencePlanDigest: credential.referencePlanDigest,
    policyVersion: credential.policyVersion,
    certificateDigest: credential.certificateDigest,
  };
  if (digest(body) !== credential.token) return false;

  if (candidate !== undefined && !("kind" in candidate)) {
    const result = candidate;
    return result.admitted &&
      credential.planDigest === result.planDigest &&
      credential.referencePlanDigest === result.referencePlanDigest &&
      credential.policyVersion === result.policyVersion &&
      credential.certificateDigest === result.certificate?.digest;
  }
  if (candidate === undefined || reference === undefined) return true;

  const planCandidate = candidate as Plan;
  const expected = credentialFor(planCandidate, reference, policy, certificate ?? {
    format: "bag-partition-certificate-v1",
    referencePlanDigest: planDigest(reference),
    candidatePlanDigest: planDigest(planCandidate),
    policyVersion: policyVersion(policy),
    domain: deriveRepresentativeDomain([reference, planCandidate], policy),
    equivalent: true,
    classes: [],
    classCount: 0,
    digest: credential.certificateDigest,
  });
  return credential.format === expected.format &&
    credential.planDigest === expected.planDigest &&
    credential.referencePlanDigest === expected.referencePlanDigest &&
    credential.policyVersion === expected.policyVersion &&
    credential.certificateDigest === expected.certificateDigest &&
    credential.token === expected.token;
}
