import type {
  CertificateCheckResult,
  DatabaseSchema,
  PartitionCertificate,
  Plan,
  SqlValue,
  TenantReadPolicy,
} from "./types.ts";
import {
  canonicalJson,
  digest,
} from "./ast.ts";
import {
  databaseClassKey,
  deriveRepresentativeDomain,
  domainsEqual,
  enumerateDomainClasses,
  DEFAULT_MAX_CLASSES,
} from "./representatives.ts";
import type { EnumeratedRepresentatives } from "./representatives.ts";
import { evaluatePlan } from "./eval.ts";
import { analyzePlans } from "./analysis.ts";
import { normalizePolicy } from "./policy.ts";

export interface CertificateCheckContext {
  readonly reference?: Plan;
  readonly referencePlan?: Plan;
  readonly candidate?: Plan;
  readonly candidatePlan?: Plan;
  readonly policy?: TenantReadPolicy;
  readonly schema?: DatabaseSchema;
  readonly maxRepresentativeClasses?: number;
  readonly checkVerdict?: boolean;
}

function normalizeCheckContext(
  context: CertificateCheckContext | Plan | undefined,
  candidate?: Plan,
  policy?: TenantReadPolicy,
  schema?: DatabaseSchema,
): CertificateCheckContext {
  if (context === undefined) return {};
  if ("kind" in context && (
    context.kind === "scan" || context.kind === "join" || context.kind === "filter" ||
    context.kind === "project" || context.kind === "unionAll"
  )) {
    return { reference: context, candidate, policy, schema };
  }
  return context;
}

function bagKey(row: readonly SqlValue[]): string {
  return JSON.stringify(row.map((value) => {
    if (value.kind === "null") return "n";
    if (value.kind === "enum") return `e:${JSON.stringify(value.value)}`;
    return `i:${JSON.stringify(value.name)}`;
  }));
}

function bagsEqual(
  left: readonly (readonly SqlValue[])[],
  right: readonly (readonly SqlValue[])[],
): boolean {
  const counts = new Map<string, number>();
  for (const row of left) counts.set(bagKey(row), (counts.get(bagKey(row)) ?? 0) + 1);
  for (const row of right) {
    const key = bagKey(row);
    const count = counts.get(key) ?? 0;
    if (count === 0) return false;
    if (count === 1) counts.delete(key);
    else counts.set(key, count - 1);
  }
  return counts.size === 0;
}

export function checkPartitionCertificate(
  certificate: PartitionCertificate,
  context?: CertificateCheckContext | Plan,
  candidate?: Plan,
  policy?: TenantReadPolicy,
  schema?: DatabaseSchema,
): CertificateCheckResult {
  const ctx = normalizeCheckContext(context, candidate, policy, schema);
  const errors: string[] = [];
  if (certificate.format !== "bag-partition-certificate-v1") {
    errors.push(`Unsupported certificate format: ${String(certificate.format)}`);
  }
  let expected: EnumeratedRepresentatives;
  try {
    expected = enumerateDomainClasses(
      certificate.domain,
      ctx.maxRepresentativeClasses ?? DEFAULT_MAX_CLASSES,
    );
  } catch (error) {
    expected = { classes: [], truncated: true };
    errors.push(`Declared domain cannot be enumerated: ${String(error)}`);
  }
  if (expected.truncated) {
    errors.push("Declared domain enumeration exceeded the representative limit");
  }

  const expectedKeys = new Set(expected.classes.map((entry) => entry.key));
  const seen = new Set<string>();
  const unexpected: string[] = [];
  const certificateClasses = Array.isArray(certificate.classes) ? certificate.classes : [];
  if (!Array.isArray(certificate.classes)) errors.push("Certificate classes are not an array");
  for (const entry of certificateClasses) {
    const actualKey = databaseClassKey(entry.database);
    if (actualKey !== entry.key) {
      errors.push(`Class ${entry.key} has a database with key ${actualKey}`);
    }
    if (seen.has(entry.key)) errors.push(`Duplicate class key ${entry.key}`);
    seen.add(entry.key);
    if (!expectedKeys.has(entry.key)) unexpected.push(entry.key);
  }

  const missing = [...expectedKeys].filter((key) => !seen.has(key)).sort();
  if (certificate.classCount !== certificateClasses.length) {
    errors.push(
      `classCount ${certificate.classCount} does not equal ${certificateClasses.length}`,
    );
  }

  let domainMatches = true;
  const reference = ctx.reference ?? ctx.referencePlan;
  const queryCandidate = ctx.candidate ?? ctx.candidatePlan;
  if (reference !== undefined || queryCandidate !== undefined) {
    if (reference === undefined || queryCandidate === undefined) {
      errors.push("Both reference and candidate plans are required for domain checking");
      domainMatches = false;
    } else {
      const rederived = deriveRepresentativeDomain(
        [reference, queryCandidate],
        ctx.policy,
        ctx.schema,
      );
      domainMatches = domainsEqual(rederived, certificate.domain);
      if (!domainMatches) {
        errors.push("Certificate domain does not match the domain derived from the plans");
      }

      if (ctx.checkVerdict === true) {
        const analysis = analyzePlans([reference, queryCandidate], ctx.policy, ctx.schema);
        const referencePolicy = normalizePolicy(ctx.policy);
        let equivalent = true;
        for (const entry of certificateClasses) {
          const referenceRows = evaluatePlan(reference, entry.database, referencePolicy.source, {
            columnsByTable: analysis.columnsByTable,
          });
          const candidateRows = evaluatePlan(queryCandidate, entry.database, undefined, {
            columnsByTable: analysis.columnsByTable,
          });
          if (!bagsEqual(referenceRows, candidateRows)) {
            equivalent = false;
            break;
          }
        }
        if (equivalent !== certificate.equivalent) {
          errors.push(
            `Certificate verdict ${certificate.equivalent} disagrees with reevaluation ${equivalent}`,
          );
        }
      }
    }
  }

  const root = {
    format: certificate.format,
    referencePlanDigest: certificate.referencePlanDigest,
    candidatePlanDigest: certificate.candidatePlanDigest,
    policyVersion: certificate.policyVersion,
    domain: certificate.domain,
    equivalent: certificate.equivalent,
    classes: certificate.classes,
    classCount: certificate.classCount,
  };
  const expectedDigest = digest(root);
  if (expectedDigest !== certificate.digest) {
    errors.push("Certificate digest mismatch");
  }

  const ok =
    missing.length === 0 &&
    unexpected.length === 0 &&
    domainMatches &&
    errors.length === 0;
  return {
    ok,
    valid: ok,
    expectedClasses: expectedKeys.size,
    checkedClasses: certificate.classes.length,
    missing,
    unexpected,
    domainMatches,
    errors,
  };
}

export const verifyPartitionCertificate = checkPartitionCertificate;
export const checkRepresentativeCoverage = checkPartitionCertificate;
export const verifyCertificateCoverage = checkPartitionCertificate;

export function isPartitionCertificateComplete(
  certificate: PartitionCertificate,
  context?: CertificateCheckContext | Plan,
  candidate?: Plan,
  policy?: TenantReadPolicy,
  schema?: DatabaseSchema,
): boolean {
  return checkPartitionCertificate(certificate, context, candidate, policy, schema).ok;
}

export const certificateCoversAllClasses = isPartitionCertificateComplete;

export function certificateSignature(certificate: PartitionCertificate): string {
  return digest(certificate);
}

export function canonicalCertificateJson(certificate: PartitionCertificate): string {
  return canonicalJson(certificate);
}
